package main

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// stage 是面试的一个阶段。阶段由后端按规则推进，模型只负责评估和提问。
type stage struct {
	Name     string
	Goal     string
	EndMin   int // 面试进行到这一分钟仍在本阶段，就进入下一阶段
	MaxTurns int // 候选人在本阶段最多回答几轮
}

var stages = []stage{
	{"了解候选人", "了解候选人想去的岗位、背景和做过的事，找到一段值得深挖的经历。不要让候选人做正式的自我介绍，在聊天中自然问出来。", 5, 3},
	{"经历深挖", "挑候选人提到的一段经历，问背后的业务目标、为什么这么做、怎么衡量效果、他自己做了哪些判断和取舍。", 15, 6},
	{"情景题", "结合方向和业务领域出一道开放题（比如设计一个功能、做一个架构决策、处理一次团队分歧），看思路和取舍，不追求标准答案。", 25, 6},
	{"反问", "问候选人有没有想问你的，简短真诚地回答。", 29, 3},
	{"收尾", "感谢候选人，告诉他稍后可以查看复盘。", 0, 0},
}

var closingStage = len(stages) - 1

// interviewState 由前端保存，每轮随请求带回，后端不存状态。
type interviewState struct {
	Stage int    `json:"stage"` // stages 下标
	Turns int    `json:"turns"` // 候选人在当前阶段已回答的轮数
	Name  string `json:"name"`  // 阶段名，只用于前端展示
}

// evaluation 是评估节点对候选人最新回答的判断。
type evaluation struct {
	StageDone  bool   `json:"stage_done"`   // 本阶段目标是否已经达成
	WantsToEnd bool   `json:"wants_to_end"` // 候选人是否明确想结束面试
	Focus      string `json:"focus"`        // 下一问应该抓住的点
}

// turn 是一轮面试在图中流转的数据。
// decide 节点写入 State，handler 收到第一段输出后读取它：此时 decide 一定已经执行完。
type turn struct {
	Req      request
	State    interviewState
	Eval     evaluation
	Moved    bool               // 本轮是否进入了新阶段
	Memories []*schema.Document // 检索到的历史面试片段
}

type turnRef struct{ t *turn }

// newInterviewGraph 编排一轮面试：
//
//	START ─有回答─▶ query ▶ retrieve ▶ remember ─▶ eval_prompt ▶ evaluate ▶ parse_eval ─▶ decide ▶ interviewer ▶ END
//	  │      └─未启用历史检索─────────────────────┘                                   │
//	  └─────开场，无需评估────────────────────────────────────────────────────────────┘
//
// memories 为 nil 时不检索历史面试。
func newInterviewGraph(ctx context.Context, evaluator, interviewer model.BaseChatModel, memories retriever.Retriever) (compose.Runnable[*turn, *schema.Message], error) {
	// 评估分支要把 *turn 带过模型和检索节点，借图的局部状态暂存。
	g := compose.NewGraph[*turn, *schema.Message](compose.WithGenLocalState(func(context.Context) *turnRef { return &turnRef{} }))
	saved := func(ctx context.Context) (t *turn, err error) {
		err = compose.ProcessState(ctx, func(_ context.Context, s *turnRef) error { t = s.t; return nil })
		return t, err
	}

	_ = g.AddLambdaNode("eval_prompt", compose.InvokableLambda(func(ctx context.Context, t *turn) ([]*schema.Message, error) {
		err := compose.ProcessState(ctx, func(_ context.Context, s *turnRef) error { s.t = t; return nil })
		return evaluatorMessages(t), err
	}))
	_ = g.AddChatModelNode("evaluate", evaluator)
	_ = g.AddLambdaNode("parse_eval", compose.InvokableLambda(func(ctx context.Context, m *schema.Message) (*turn, error) {
		t, err := saved(ctx)
		if err != nil {
			return nil, err
		}
		if jerr := json.Unmarshal([]byte(stripFence(m.Content)), &t.Eval); jerr != nil {
			log.Printf("evaluate: bad json %q: %v", m.Content, jerr) // 评估失败不影响面试，只是本轮不靠评估推进
		}
		return t, nil
	}))
	_ = g.AddLambdaNode("decide", compose.InvokableLambda(func(_ context.Context, t *turn) ([]*schema.Message, error) {
		decide(t)
		return interviewerMessages(t), nil
	}))
	_ = g.AddChatModelNode("interviewer", interviewer)

	if memories != nil {
		// 用最近一问一答去检索，比只用回答更能命中同一话题。
		_ = g.AddLambdaNode("query", compose.InvokableLambda(func(ctx context.Context, t *turn) (string, error) {
			err := compose.ProcessState(ctx, func(_ context.Context, s *turnRef) error { s.t = t; return nil })
			recent := transcript(t.Req.Messages)
			return clip(transcriptText(recent[max(len(recent)-2, 0):]), 500), err
		}))
		_ = g.AddRetrieverNode("retrieve", softRetriever{memories})
		_ = g.AddLambdaNode("remember", compose.InvokableLambda(func(ctx context.Context, docs []*schema.Document) (*turn, error) {
			t, err := saved(ctx)
			if t != nil {
				t.Memories = docs
			}
			return t, err
		}))
		_ = g.AddEdge("query", "retrieve")
		_ = g.AddEdge("retrieve", "remember")
		_ = g.AddEdge("remember", "eval_prompt")
	}

	ends := map[string]bool{"eval_prompt": true, "decide": true}
	if memories != nil {
		ends["query"] = true
	}
	_ = g.AddBranch(compose.START, compose.NewGraphBranch(func(_ context.Context, t *turn) (string, error) {
		switch {
		case !hasUserMessage(t.Req.Messages):
			return "decide", nil
		case memories != nil && t.Req.User != "":
			return "query", nil
		}
		return "eval_prompt", nil
	}, ends))
	_ = g.AddEdge("eval_prompt", "evaluate")
	_ = g.AddEdge("evaluate", "parse_eval")
	_ = g.AddEdge("parse_eval", "decide")
	_ = g.AddEdge("decide", "interviewer")
	_ = g.AddEdge("interviewer", compose.END)

	return g.Compile(ctx, compose.WithGraphName("interview"))
}

// decide 按评估结果、轮数和时间决定本轮所处的阶段。
func decide(t *turn) {
	s := interviewState{}
	if t.Req.State != nil {
		s = *t.Req.State
	}
	s.Stage = min(max(s.Stage, 0), closingStage)

	if prev := s.Stage; hasUserMessage(t.Req.Messages) && s.Stage != closingStage {
		s.Turns++
		cur := stages[s.Stage]
		switch {
		case t.Eval.WantsToEnd:
			s.Stage = closingStage
		case t.Eval.StageDone, s.Turns >= cur.MaxTurns, t.Req.Minutes >= cur.EndMin:
			s.Stage++
			// 时间不够时跳过已经超时的阶段，直接进入反问 / 收尾。
			for s.Stage < closingStage && t.Req.Minutes >= stages[s.Stage].EndMin {
				s.Stage++
			}
		}
		if s.Stage != prev {
			s.Turns = 0
			t.Moved = true
		}
	}
	s.Name = stages[s.Stage].Name
	t.State = s
}

// stripFence 去掉模型偶尔包在 JSON 外面的 ``` 代码块。
func stripFence(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	return strings.TrimSpace(strings.TrimSuffix(s, "```"))
}
