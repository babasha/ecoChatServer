package adkagent

import (
	"fmt"
	"log"
	"sync/atomic"

	"github.com/egor/ecochatserver/agentbus"
	"github.com/egor/ecochatserver/database"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

const maxDirectorCalls = 2

// askDirectorInput is the input schema for the ask_director tool.
type askDirectorInput struct {
	Question string `json:"question"`
}

// askDirectorOutput is the output schema for the ask_director tool.
type askDirectorOutput struct {
	Result string `json:"result"`
}

// createAskDirectorTool creates the ask_director ADK tool that lets L1 agents
// query the Director for strategic guidance. Rate-limited to maxDirectorCalls per message.
func createAskDirectorTool(bus *agentbus.AgentBus, callCount *atomic.Int32) tool.Tool {
	t, err := functiontool.New(
		functiontool.Config{
			Name:        "ask_director",
			Description: "Ask the Director (strategic AI supervisor) for guidance on complex customer issues. Use when you are unsure how to handle a difficult situation, need advice on escalation, or want strategic input. Maximum 2 uses per conversation turn.",
		},
		func(ctx tool.Context, input askDirectorInput) (askDirectorOutput, error) {
			// Rate limit check
			current := callCount.Add(1)
			if current > int32(maxDirectorCalls) {
				log.Printf("[ASK_DIRECTOR] Rate limit exceeded: %d/%d calls", current, maxDirectorCalls)
				return askDirectorOutput{
					Result: fmt.Sprintf("Rate limit reached: you can only ask the Director %d times per message. Please proceed with your best judgment.", maxDirectorCalls),
				}, nil
			}

			if input.Question == "" {
				return askDirectorOutput{Result: "Error: question is required."}, nil
			}

			log.Printf("[ASK_DIRECTOR] L1 agent asking Director: %s", input.Question)

			result, err := bus.QueryDirector(ctx, input.Question, "")
			if err != nil {
				log.Printf("[ASK_DIRECTOR] Error: %v", err)
				return askDirectorOutput{
					Result: "The Director is currently unavailable. Please proceed with your best judgment and escalate if needed.",
				}, nil
			}

			log.Printf("[ASK_DIRECTOR] Director response: %d chars", len(result.Response))

			// Log the conversation
			tokIn, tokOut := 0, 0
			if result.Tokens != nil {
				tokIn = result.Tokens.PromptTokens
				tokOut = result.Tokens.CompletionTokens
			}
			go database.InsertDirectorAgentConversation(
				"", "agent_to_director", "agent", input.Question, result.Response, tokIn, tokOut,
			)

			return askDirectorOutput{Result: result.Response}, nil
		},
	)
	if err != nil {
		log.Printf("[ASK_DIRECTOR] Failed to create tool: %v", err)
		return nil
	}
	return t
}
