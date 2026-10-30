package main

import (
	"fmt"
	"strings"
)

const interviewMinutes = 30

func interviewMessages(req request) []message {
	msgs := []message{{Role: "system", Content: interviewerPrompt(req)}}
	for _, m := range req.Messages {
		if m.Role == "user" || m.Role == "assistant" {
			msgs = append(msgs, m)
		}
	}
	if len(msgs) == 1 {
		// 部分模型要求至少一条 user 消息，用它让面试官先开口。
		msgs = append(msgs, message{Role: "user", Content: "（候选人已进入面试间）"})
	}
	return msgs
}

// reviewMessages 把整场对话作为一份记录交给复盘教练。
// 候选人一句话都没说时返回 nil，不调用模型。
func reviewMessages(req request) []message {
	if !hasUserMessage(req.Messages) {
		return nil
	}
	var b strings.Builder
	for _, m := range req.Messages {
		who := "候选人"
		if m.Role == "assistant" {
			who = "面试官"
		}
		fmt.Fprintf(&b, "%s：%s\n\n", who, m.Content)
	}
	return []message{
		{Role: "system", Content: reviewPrompt},
		{Role: "user", Content: b.String()},
	}
}

func hasUserMessage(msgs []message) bool {
	for _, m := range msgs {
		if m.Role == "user" && strings.TrimSpace(m.Content) != "" {
			return true
		}
	}
	return false
}

func interviewerPrompt(req request) string {
	langs := req.Setup.Primary
	if req.Setup.Secondary != "" {
		langs += "（主），" + req.Setup.Secondary + "（副）"
	}
	return fmt.Sprintf(`你是一位资深的%s技术面试官，正在面一位实习 / 校招候选人。候选人的编程语言：%s。

这是一场约 %d 分钟的文字面试，现在已经进行了 %d 分钟。

面试要覆盖这些内容，但不要按清单走，跟着对话自然推进：
- 了解候选人：想面的岗位、背景、做过的项目。不要一上来就让候选人"做个自我介绍"，而是在聊天中一点点问出来。
- 项目深挖：挑候选人提到的一个项目，问设计、难点、取舍、数据。
- 基础：结合岗位和语言问基础知识。
- 反问：给候选人提问的机会，简短回答。
- 收尾：感谢候选人，最后单独一行输出 [END]。
到时间后尽快进入反问和收尾。

开场只说一两句：简单打个招呼，再问一个轻松的问题（比如最近在准备什么方向的岗位），让候选人先放松。

规则：
- 像真人面试官说话：口语、简短，每次只问一个问题，不要列清单，不要用 Markdown。
- 顺着候选人的回答追问：抓住其中的关键词、含糊之处和薄弱点往下问，不要急着换下一题。
- 回答像在背书时，追问细节、场景或"为什么"来确认是否真懂。
- 面试中不要讲解、不要给答案、不要打分，这些留到复盘。
- 候选人主动提问或换了话题（比如问职业规划），先认真回应，再顺势把话题带回面试，不要无视问题、生硬地按自己的节奏走。
- 候选人可能打断你：你上一条消息可能只说了一半，直接回应候选人新说的话即可。`,
		req.Setup.Industry, langs, interviewMinutes, req.Minutes)
}

const reviewPrompt = `你是一位面试复盘教练。下面是一场技术面试的完整记录，请写复盘报告。

先用两三句话给出整体评价。然后逐个列出面试官问过的主要问题，每题包含：
- 问题：一句话概括
- 得分：0-10
- 理由：候选人答得好和不好的地方，具体到回答内容
- 更好的答法：给出一段可以直接参考的回答
最后列出最该优先补的三个弱项。

只点评候选人实际回答过的问题，不要编造没发生的内容。候选人回答很少时，报告也要相应简短，只写几句话说明能看出的情况。

用纯文本输出，不要用 Markdown 标题或加粗。`
