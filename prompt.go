package main

import (
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"
)

const interviewMinutes = 30

// transcript 只保留面试双方的对话。
func transcript(msgs []*schema.Message) []*schema.Message {
	var out []*schema.Message
	for _, m := range msgs {
		if m != nil && (m.Role == schema.User || m.Role == schema.Assistant) {
			out = append(out, &schema.Message{Role: m.Role, Content: m.Content})
		}
	}
	return out
}

func transcriptText(msgs []*schema.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		who := "候选人"
		if m.Role == schema.Assistant {
			who = "面试官"
		}
		fmt.Fprintf(&b, "%s：%s\n\n", who, m.Content)
	}
	return b.String()
}

func hasUserMessage(msgs []*schema.Message) bool {
	for _, m := range msgs {
		if m != nil && m.Role == schema.User && strings.TrimSpace(m.Content) != "" {
			return true
		}
	}
	return false
}

// evaluatorMessages 让评估模型判断候选人的最新回答。只带最近几轮，省 token。
func evaluatorMessages(t *turn) []*schema.Message {
	s := interviewState{}
	if t.Req.State != nil {
		s = *t.Req.State
	}
	cur := flowFor(t.Req.Setup.Role).stageAt(s.Stage)
	recent := transcript(t.Req.Messages)
	recent = recent[max(len(recent)-8, 0):]

	return []*schema.Message{
		schema.SystemMessage(fmt.Sprintf(`你在旁听一场%s方向的面试，帮面试官判断进度。候选人阶段：%s。
%s
当前阶段：%s。阶段目标：%s
候选人在本阶段已经回答了 %d 轮（不含最新一条）。
%s
根据最近的对话，只输出一个 JSON 对象，不要输出其他内容：
{"stage_done": 布尔值, "wants_to_end": 布尔值, "focus": "字符串"}

- stage_done：本阶段目标是否已经达成，可以进入下一阶段。候选人的回答还有明显值得追问的地方时为 false。
- wants_to_end：候选人是否明确表示想结束面试。
- focus：面试官下一问最该抓住的点，一句话，比如候选人回答里的含糊之处、没说清的取舍、和简历或以前说法对不上的地方、职位要求里还没考察到的能力、或者候选人提出的问题。`,
			t.Req.Setup.Role, t.Req.Setup.Level, profileBlock(t.Req.Profile), cur.Name, cur.Goal, s.Turns, memoriesBlock(t.Memories))),
		schema.UserMessage(transcriptText(recent)),
	}
}

// profileBlock 把简历和职位描述原文交给模型。放在提示词前部且每轮不变，便于模型服务缓存前缀。
func profileBlock(p profile) string {
	if p.Resume == "" && p.JD == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n以下是候选人提供的材料，是这场面试的事实依据：\n")
	if p.Resume != "" {
		fmt.Fprintf(&b, "\n【候选人简历】\n%s\n", p.Resume)
	}
	if p.JD != "" {
		fmt.Fprintf(&b, "\n【目标职位描述】\n%s\n", p.JD)
	}
	return b.String()
}

var memoryKinds = map[string]string{"qa": "问答", "report": "复盘"}

// memoriesBlock 列出检索到的历史面试片段。
func memoriesBlock(docs []*schema.Document) string {
	if len(docs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n这位候选人以前面试中和当前话题相关的片段（仅供参考，和当前话题无关就忽略）：\n")
	for _, d := range docs {
		fmt.Fprintf(&b, "- [%v %v · %s] %s\n", d.MetaData["date"], d.MetaData["role"], memoryKinds[fmt.Sprint(d.MetaData["kind"])],
			strings.ReplaceAll(d.Content, "\n", " / "))
	}
	return b.String()
}

// roleFocus 是每个面试方向重点考察的内容；前端的方向选项要和这里的 key 一致。
var roleFocus = map[string]string{
	"产品经理":    "用户和场景洞察、需求的优先级取舍、商业模式和关键指标、如何推动研发和业务落地。",
	"AI 产品经理": "判断哪些问题适合用 AI 解决、模型能力边界和成本、效果怎么评估、AI 产品的体验设计。",
	"技术架构师":   "从业务目标推导架构、关键取舍（一致性、成本、扩展性、交付速度）、架构如何随业务演进。",
	"技术负责人":   "把业务目标拆成技术计划、团队分工和带人、技术债和交付节奏的平衡、跨部门沟通。",
	"全栈开发":    "端到端交付一个功能、从用户体验到数据的整体思考、如何借助 AI 工具提升产出。",
	"AI 应用开发": "把业务问题落成 AI 应用、提示词 / 检索 / 智能体等方案的选择、效果评估和成本、上线后的迭代。",
}

func interviewerMessages(t *turn) []*schema.Message {
	msgs := append([]*schema.Message{schema.SystemMessage(interviewerPrompt(t))}, transcript(t.Req.Messages)...)
	if len(msgs) == 1 {
		// 部分模型要求至少一条 user 消息，用它让面试官先开口。
		msgs = append(msgs, schema.UserMessage("（候选人已进入面试间）"))
	}
	return msgs
}

func interviewerPrompt(t *turn) string {
	s := t.Req.Setup
	focus := roleFocus[s.Role]
	if focus == "" {
		focus = "候选人对产品和业务的理解、判断力和落地能力。"
	}
	domain := s.Domain
	if domain == "" {
		domain = "不限"
	}
	f := flowFor(s.Role)
	cur := f.stageAt(t.State.Stage)

	var now strings.Builder
	fmt.Fprintf(&now, "当前阶段：%s。阶段目标：%s\n", cur.Name, cur.Goal)
	switch {
	case !hasUserMessage(t.Req.Messages):
		fmt.Fprintf(&now, "这是开场：只说一两句，简单打个招呼，然后%s。候选人提供了简历的话，可以从简历里挑一段和这个方向相关的经历来问。语气轻松，让候选人先放松。\n", f.Opening)
	case t.State.Stage == f.closing():
		now.WriteString("先简短回应候选人刚才的话，然后感谢候选人、结束面试，不要再提新问题。最后单独一行输出 [END]。\n")
	case t.Moved:
		now.WriteString("这一轮刚进入本阶段：先对候选人刚才的话做个自然的回应，再用一句话过渡到本阶段的第一个问题。\n")
	}
	if t.Eval.Focus != "" && !t.Moved {
		fmt.Fprintf(&now, "旁听的同事建议这一问抓住：%s\n", t.Eval.Focus)
	}

	material := profileBlock(t.Req.Profile)
	if material != "" {
		material += "\n用法：从简历里挑经历来问，对照职位描述的要求考察。简历上写的成果要追问细节核实，不要照着简历念，也不要一开口就复述简历。\n"
	}
	if m := memoriesBlock(t.Memories); m != "" {
		now.WriteString(m)
		now.WriteString("用法：不要重复以前问过的问题；以前答得弱的点可以换个角度再考察，看看有没有进步；和以前说法矛盾的地方可以追问。可以自然地提一句\"上次聊到……\"，但不要逐条复述。\n")
	}

	// 不变的部分在前、每轮变化的部分在后，便于模型服务缓存提示词前缀。
	return fmt.Sprintf(`你是一位资深面试官，正在面一位%s方向的候选人。候选人阶段：%s。业务领域：%s。

这个方向重点考察：%s
不考记忆型的知识点和八股文。技术细节只在能看出候选人思考深度时追问，更看重他是否理解业务、能否做出有理由的判断。问题的难度和期待要符合候选人的阶段。
%s
规则：
- 像真人面试官说话：口语、简短，每次只问一个问题，不要列清单，不要用 Markdown。
- 顺着候选人的回答追问：抓住其中的关键词、含糊之处和薄弱点往下问，不要急着换下一题。
- 回答空泛或像在背模板时，追问具体场景、数据或"为什么"来确认是否真想清楚了。
- 面试中不要讲解、不要给答案、不要打分，这些留到复盘。
- 候选人主动提问或换了话题（比如问职业规划），先认真回应，再顺势把话题带回面试，不要无视问题、生硬地按自己的节奏走。
- 候选人可能打断你：你上一条消息可能只说了一半，直接回应候选人新说的话即可。
- 只有在收尾阶段才输出 [END]。

这是一场约 %d 分钟的文字面试，现在已经进行了 %d 分钟。面试分为：%s，阶段切换由系统控制，你只需要专注当前阶段。

%s`,
		s.Role, s.Level, domain, focus, material, interviewMinutes, t.Req.Minutes, f.names(), now.String())
}

// reviewMessages 把整场对话作为一份记录交给复盘教练。
func reviewMessages(req request) []*schema.Message {
	return []*schema.Message{
		schema.SystemMessage(reviewPrompt + profileBlock(req.Profile)),
		schema.UserMessage(transcriptText(transcript(req.Messages))),
	}
}

const reviewPrompt = `你是一位面试复盘教练。下面是一场面试的完整记录，请写复盘报告。

先用两三句话给出整体评价。然后逐个列出面试官问过的主要问题，每题包含：
- 问题：一句话概括
- 得分：0-10
- 理由：候选人答得好和不好的地方，具体到回答内容，重点看业务理解和判断是否有理有据
- 更好的答法：给出一段可以直接参考的回答
最后列出最该优先补的三个弱项。

如果提供了职位描述，最后再对照职位要求，指出候选人还有哪些差距。

只点评候选人实际回答过的问题，不要编造没发生的内容。候选人回答很少时，报告也要相应简短，只写几句话说明能看出的情况。

用纯文本输出，不要用 Markdown 标题或加粗。`
