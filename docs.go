package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"strings"
	"unicode"

	"github.com/cloudwego/eino-ext/components/document/parser/pdf"
	"github.com/cloudwego/eino/components/document/parser"
	"github.com/cloudwego/eino/schema"
	"golang.org/x/text/unicode/norm"
)

// maxProfileRunes 限制简历 / 职位描述的长度，避免一份异常文档撑爆提示词。
const maxProfileRunes = 8000

// newDocParser 按扩展名选择解析器：PDF、Word，其他按纯文本读。
func newDocParser(ctx context.Context) (parser.Parser, error) {
	p, err := pdf.NewPDFParser(ctx, &pdf.Config{})
	if err != nil {
		return nil, err
	}
	return parser.NewExtParser(ctx, &parser.ExtParserConfig{
		Parsers: map[string]parser.Parser{".pdf": p, ".docx": docxParser{}},
	})
}

// handleParse 把上传的简历 / 职位描述文件解析成纯文本返回，文件本身不保存。
func handleParse(p parser.Parser) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
		f, h, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "请上传 10MB 以内的文件", http.StatusBadRequest)
			return
		}
		defer f.Close()
		name := strings.ToLower(h.Filename)
		docs, err := safeParse(r.Context(), p, f, name)
		if err != nil {
			http.Error(w, "解析失败："+err.Error(), http.StatusUnprocessableEntity)
			return
		}
		var parts []string
		for _, d := range docs {
			parts = append(parts, d.Content)
		}
		text := fixRadicals(strings.Join(parts, "\n"))
		if path.Ext(name) == ".pdf" {
			text = joinFragments(text)
		}
		text = clip(cleanText(text), maxProfileRunes)
		if text == "" {
			http.Error(w, "没有读到文字，可能是扫描版 PDF，请直接粘贴文字", http.StatusUnprocessableEntity)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"text": text})
	}
}

// safeParse 调用解析器，第三方解析库遇到异常文件 panic 时转成错误。
func safeParse(ctx context.Context, p parser.Parser, r io.Reader, name string) (docs []*schema.Document, err error) {
	defer func() {
		if v := recover(); v != nil {
			log.Printf("parse %s: panic: %v", name, v)
			err = fmt.Errorf("文件格式无法识别")
		}
	}()
	return p.Parse(ctx, r, parser.WithURI(name))
}

// joinFragments 把 PDF 解析出的碎片行在段落内拼回一行：PDF 里每个文字块单独成行，中文会被拆得很碎。
// 两侧都是字母或数字时补一个空格，其余直接相连。
func joinFragments(s string) string {
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" { // 空行（可能只含空格）是段落分隔
			flush()
			continue
		}
		if b.Len() > 0 {
			prev := []rune(b.String())
			if isWordRune(prev[len(prev)-1]) && isWordRune([]rune(line)[0]) {
				b.WriteByte(' ')
			}
		}
		b.WriteString(line)
	}
	flush()
	return strings.Join(out, "\n")
}

// fixRadicals 把 PDF 里常见的康熙部首、兼容汉字（如"⼯""⽉"）换成常用字。
// 只处理这几个区段：对全文做 NFKC 会把中文全角标点也变成半角。
func fixRadicals(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 0x2E80 && r <= 0x2FDF) || (r >= 0xF900 && r <= 0xFAFF) {
			if n := []rune(norm.NFKC.String(string(r))); len(n) == 1 {
				return n[0]
			}
		}
		return r
	}, s)
}

func isWordRune(r rune) bool {
	return r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r))
}

// docxParser 从 Word 文件里按段落取出文字。只读 word/document.xml，不依赖样式等其他部件，
// 所以 Pages、WPS、textutil 等导出的不完整 docx 也能读。实现 Eino 的 parser.Parser 接口。
type docxParser struct{}

func (docxParser) Parse(_ context.Context, r io.Reader, _ ...parser.Option) ([]*schema.Document, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("不是有效的 docx 文件")
	}
	f, err := zr.Open("word/document.xml")
	if err != nil {
		return nil, fmt.Errorf("docx 里没有正文")
	}
	defer f.Close()

	var b strings.Builder
	dec := xml.NewDecoder(f)
	inText := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				inText = true
			case "tab":
				b.WriteByte('\t')
			case "br":
				b.WriteByte('\n')
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "p":
				b.WriteByte('\n')
			case "tc": // 表格单元格之间
				b.WriteByte('\t')
			}
		case xml.CharData:
			if inText {
				b.Write(t)
			}
		}
	}
	return []*schema.Document{{Content: b.String()}}, nil
}

// cleanText 去掉每行首尾空白和连续空行。
func cleanText(s string) string {
	var out []string
	blank := false
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if !blank && len(out) > 0 {
				out = append(out, "")
			}
			blank = true
			continue
		}
		out = append(out, line)
		blank = false
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
