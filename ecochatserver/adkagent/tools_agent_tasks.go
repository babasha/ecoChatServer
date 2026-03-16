package adkagent

import (
	"fmt"
	"log"
	"strings"

	"github.com/egor/ecochatserver/database"
	"github.com/google/uuid"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// ============================================================================
// Agent task tools — allow L1 agents to see and act on tasks from Director
// ============================================================================

type getMyTasksInput struct{}
type getMyTasksOutput struct {
	Result string `json:"result"`
}

func createGetMyTasksTool() tool.Tool {
	t, err := functiontool.New(
		functiontool.Config{
			Name:        "get_my_tasks",
			Description: "Get your assigned tasks from the Director. Shows pending and in-progress tasks you need to complete. Check this when idle or when the Director assigns you work.",
		},
		func(ctx tool.Context, input getMyTasksInput) (getMyTasksOutput, error) {
			tasks, err := database.GetTasksForAgent()
			if err != nil {
				log.Printf("[AGENT_TASKS] Error: %v", err)
				return getMyTasksOutput{Result: "Error loading tasks."}, nil
			}

			if len(tasks) == 0 {
				return getMyTasksOutput{Result: "No tasks assigned to you. All clear!"}, nil
			}

			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("You have %d assigned tasks:\n\n", len(tasks)))
			for i, t := range tasks {
				sb.WriteString(fmt.Sprintf("%d. [%s/%s] %s\n", i+1, t.Priority, t.Status, t.Title))
				sb.WriteString(fmt.Sprintf("   ID: %s\n", t.ID))
				if t.Description != "" {
					desc := t.Description
					if len(desc) > 200 {
						desc = desc[:200] + "..."
					}
					sb.WriteString(fmt.Sprintf("   %s\n", desc))
				}
				sb.WriteString("\n")
			}
			sb.WriteString("Use complete_task to mark a task as done, or ask_director for guidance.")

			return getMyTasksOutput{Result: sb.String()}, nil
		},
	)
	if err != nil {
		log.Printf("[AGENT_TASKS] Failed to create get_my_tasks tool: %v", err)
		return nil
	}
	return t
}

type completeTaskInput struct {
	TaskID  string `json:"task_id"`
	Status  string `json:"status"`
	Comment string `json:"comment"`
	Reason  string `json:"reason"`
}

type completeTaskOutput struct {
	Result string `json:"result"`
}

func createCompleteTaskTool() tool.Tool {
	t, err := functiontool.New(
		functiontool.Config{
			Name:        "complete_task",
			Description: "Mark an assigned task as completed or failed. Always provide a comment explaining what you did or why it failed. Status: 'completed' (done successfully) or 'failed' (could not complete, give reason).",
		},
		func(ctx tool.Context, input completeTaskInput) (completeTaskOutput, error) {
			if input.TaskID == "" {
				return completeTaskOutput{Result: "Error: task_id is required."}, nil
			}

			taskID, err := uuid.Parse(input.TaskID)
			if err != nil {
				return completeTaskOutput{Result: fmt.Sprintf("Error: invalid task_id: %v", err)}, nil
			}

			status := input.Status
			if status == "" {
				status = "completed"
			}
			if status != "completed" && status != "failed" && status != "in_progress" {
				return completeTaskOutput{Result: "Error: status must be 'completed', 'failed', or 'in_progress'."}, nil
			}

			failureReason := input.Reason
			if status == "failed" && failureReason == "" {
				failureReason = "No reason provided"
			}

			if err := database.UpdateTaskStatus(taskID, status, failureReason); err != nil {
				return completeTaskOutput{Result: fmt.Sprintf("Error: %v", err)}, nil
			}

			// Add comment
			comment := input.Comment
			if comment == "" {
				comment = fmt.Sprintf("Agent set status to %s", status)
			}
			database.AddTaskComment(taskID, "agent", fmt.Sprintf("[%s] %s", status, comment))

			log.Printf("[AGENT_TASKS] Task %s → %s by agent", input.TaskID[:8], status)
			return completeTaskOutput{Result: fmt.Sprintf("Task %s updated to '%s'. Comment saved.", input.TaskID[:8], status)}, nil
		},
	)
	if err != nil {
		log.Printf("[AGENT_TASKS] Failed to create complete_task tool: %v", err)
		return nil
	}
	return t
}
