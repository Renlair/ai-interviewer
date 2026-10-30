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

// roleFocus 是每个面试方向重点考察的内容；前端的方向选项要和这里的 key 一致。
var roleFocus = map[string]string{
	"产品经理":    "用户和场景洞察、需求的优先级取舍、商业模式和关键指标、如何推动研发和业务落地。",
	"AI 产品经理": "判断哪些问题适合用 AI 解决、模型能力边界和成本、效果怎么评估、AI 产品的体验设计。",
	"技术架构师":   "从业务目标推导架构、关键取舍（一致性、成本、扩展性、交付速度）、架构如何随业务演进。",
	"技术负责人":   "把业务目标拆成技术计划、团队分工和带人、技术债和交付节奏的平衡、跨部门沟通。",
	"全栈开发":    "端到端交付一个功能、从用户体验到数据的整体思考、如何借助 AI 工具提升产出。",
	"AI 应用开发": "把业务问题落成 AI 应用、提示词 / 检索 / 智能体等方案的选择、效果评估和成本、上线后的迭代。",
}

func interviewerPrompt(req request) string {
	s := req.Setup
	focus := roleFocus[s.Role]
	if focus == "" {
		focus = "候选人对产品和业务的理解、判断力和落地能力。"
	}
	domain := s.Domain
	if domain == "" {
		domain = "不限"
	}
	return fmt.Sprintf(`你是一位资深面试官，正在面一位%s方向的候选人。候选人阶段：%s。业务领域：%s。

这个方向重点考察：%s
不考记忆型的知识点和八股文。技术细节只在能看出候选人思考深度时追问，更看重他是否理解业务、能否做出有理由的判断。

这是一场约 %d 分钟的文字面试，现在已经进行了 %d 分钟。

面试要覆盖这些内容，但不要按清单走，跟着对话自然推进：
- 了解候选人：想去的岗位、背景、做过的事。不要一上来就让候选人"做个自我介绍"，而是在聊天中一点点问出来。
- 经历深挖：挑候选人提到的一段经历，问背后的业务目标、为什么这么做、怎么衡量效果、他自己做了哪些判断和取舍。
- 情景题：结合方向和业务领域出一道开放题（比如设计一个功能、做一个架构决策、处理一次团队分歧），看思路和取舍，不追求标准答案。
- 反问：给候选人提问的机会，简短回答。
- 收尾：感谢候选人，最后单独一行输出 [END]。
到时间后尽快进入反问和收尾。问题的难度和期待要符合候选人的阶段。

开场只说一两句：简单打个招呼，再问一个轻松的问题（比如最近在关注什么样的机会），让候选人先放松。

规则：
- 像真人面试官说话：口语、简短，每次只问一个问题，不要列清单，不要用 Markdown。
- 顺着候选人的回答追问：抓住其中的关键词、含糊之处和薄弱点往下问，不要急着换下一题。
- 回答空泛或像在背模板时，追问具体场景、数据或"为什么"来确认是否真想清楚了。
- 面试中不要讲解、不要给答案、不要打分，这些留到复盘。
- 候选人主动提问或换了话题（比如问职业规划），先认真回应，再顺势把话题带回面试，不要无视问题、生硬地按自己的节奏走。
- 候选人可能打断你：你上一条消息可能只说了一半，直接回应候选人新说的话即可。`,
		s.Role, s.Level, domain, focus, interviewMinutes, req.Minutes)
}

const reviewPrompt = `你是一位面试复盘教练。下面是一场面试的完整记录，请写复盘报告。

先用两三句话给出整体评价。然后逐个列出面试官问过的主要问题，每题包含：
- 问题：一句话概括
- 得分：0-10
- 理由：候选人答得好和不好的地方，具体到回答内容，重点看业务理解和判断是否有理有据
- 更好的答法：给出一段可以直接参考的回答
最后列出最该优先补的三个弱项。

只点评候选人实际回答过的问题，不要编造没发生的内容。候选人回答很少时，报告也要相应简短，只写几句话说明能看出的情况。

用纯文本输出，不要用 Markdown 标题或加粗。`
