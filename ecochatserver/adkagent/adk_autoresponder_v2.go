package adkagent

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/egor/ecochatserver/llm"
	"github.com/egor/ecochatserver/models"
)

// ADKAutoResponderV2 - ╨╛╨▒╨╜╨╛╨▓╨╗╤æ╨╜╨╜╨░╤Å ╨▓╨╡╤Ç╤ü╨╕╤Å ╤ü ╨┐╤Ç╨░╨▓╨╕╨╗╤î╨╜╤ï╨╝ ADK API
type ADKAutoResponderV2 struct {
	storeClient *llm.StoreClient
	config      llm.AutoResponderConfig

	// ╨Ü╤ì╤ê ╨░╨│╨╡╨╜╤é╨╛╨▓ (2 ╤ì╨║╨╖╨╡╨╝╨┐╨╗╤Å╤Ç╨░: ╨┤╨╗╤Å ╨░╨▓╤é╨╛╤Ç╨╕╨╖╨╛╨▓╨░╨╜╨╜╤ï╤à ╨╕ ╨│╨╛╤ü╤é╨╡╨╣)
	agentsMu            sync.RWMutex
	authorizedAgent     *SupportAgent   // Agent ╨┤╨╗╤Å ╨╖╨░╨╗╨╛╨│╨╕╨╜╨╡╨╜╨╜╤ï╤à ╨┐╨╛╨╗╤î╨╖╨╛╨▓╨░╤é╨╡╨╗╨╡╨╣ (19 tools)
	unauthorizedAgent   *SupportAgent   // Agent ╨┤╨╗╤Å ╨│╨╛╤ü╤é╨╡╨╣ (13 public tools)

	// ╨¡╤ü╨║╨░╨╗╨░╤å╨╕╨╕ (in-memory - ╨┐╤Ç╨╕ ╤Ç╨╡╤ü╤é╨░╤Ç╤é╨╡ ╤ü╨▒╤Ç╨░╤ü╤ï╨▓╨░╤Ä╤é╤ü╤Å)
	// NOTE: ╨¡╤é╨╛ intentional - ╤ì╤ü╨║╨░╨╗╨░╤å╨╕╤Å ╤ì╤é╨╛ ephemeral state.
	// ╨ƒ╤Ç╨╕ ╤Ç╨╡╤ü╤é╨░╤Ç╤é╨╡ ╤ü╨╡╤Ç╨▓╨╡╤Ç╨░ ╨░╨▓╤é╨╛╨╛╤é╨▓╨╡╤é╤ç╨╕╨║ ╤ü╨╜╨╛╨▓╨░ ╨╜╨░╤ç╨╜╤æ╤é ╨╛╤é╨▓╨╡╤ç╨░╤é╤î, ╤ç╤é╨╛ ╨┐╤Ç╨░╨▓╨╕╨╗╤î╨╜╨╛.
	escalationsMu sync.RWMutex
	escalations   map[string]*EscalationState
}

// NewADKAutoResponderV2 ╤ü╨╛╨╖╨┤╨░╤æ╤é ╨░╨▓╤é╨╛╨╛╤é╨▓╨╡╤é╤ç╨╕╨║ ╨╜╨░ ╨▒╨░╨╖╨╡ ADK V2
func NewADKAutoResponderV2(ctx context.Context, cfg llm.AutoResponderConfig) (*ADKAutoResponderV2, error) {
	storeClient := llm.NewStoreClient()

	ar := &ADKAutoResponderV2{
		storeClient: storeClient,
		config:      cfg,
		escalations: make(map[string]*EscalationState),
	}

	log.Printf("[ADK_V2] ╨ÿ╨╜╨╕╤å╨╕╨░╨╗╨╕╨╖╨╕╤Ç╨╛╨▓╨░╨╜ (lazy agent creation)")
	return ar, nil
}

// ProcessMessage - ╨╛╤ü╨╜╨╛╨▓╨╜╨╛╨╣ ╨╝╨╡╤é╨╛╨┤ ╨╛╨▒╤Ç╨░╨▒╨╛╤é╨║╨╕ ╤ü╨╛╨╛╨▒╤ë╨╡╨╜╨╕╤Å ╨┐╨╛╨╗╤î╨╖╨╛╨▓╨░╤é╨╡╨╗╤Å
func (ar *ADKAutoResponderV2) ProcessMessage(ctx context.Context, chat *models.Chat, msg *models.Message) (*models.Message, error) {
	log.Printf("[ADK_V2] ProcessMessage: chatID=%s", chat.ID)

	// ╨æ╤ï╤ü╤é╤Ç╤ï╨╡ ╨┐╤Ç╨╛╨▓╨╡╤Ç╨║╨╕: ╨┤╨╛╨╗╨╢╨╡╨╜ ╨╗╨╕ ╨░╨▓╤é╨╛╨╛╤é╨▓╨╡╤é╤ç╨╕╨║ ╨╛╤é╨▓╨╡╤ç╨░╤é╤î?
	if !ar.config.Enabled ||                              // ╨É╨▓╤é╨╛╨╛╤é╨▓╨╡╤é╤ç╨╕╨║ ╨▓╤ï╨║╨╗╤Ä╤ç╨╡╨╜
		msg.Sender != "user" ||                            // ╨í╨╛╨╛╨▒╤ë╨╡╨╜╨╕╨╡ ╨╜╨╡ ╨╛╤é ╨┐╨╛╨╗╤î╨╖╨╛╨▓╨░╤é╨╡╨╗╤Å
		(chat.AssignedTo != nil && *chat.AssignedTo != uuid.Nil) || // ╨º╨░╤é ╨╜╨░╨╖╨╜╨░╤ç╨╡╨╜ ╨╛╨┐╨╡╤Ç╨░╤é╨╛╤Ç╤â
		!chat.AutoResponderEnabled {                       // ╨É╨▓╤é╨╛╨╛╤é╨▓╨╡╤é╤ç╨╕╨║ ╨╛╤é╨║╨╗╤Ä╤ç╨╡╨╜ ╨┤╨╗╤Å ╤ç╨░╤é╨░
		return nil, nil
	}

	chatKey := chat.ID.String()

	// ╨ƒ╤Ç╨╛╨▓╨╡╤Ç╨║╨░ ╤ì╤ü╨║╨░╨╗╨░╤å╨╕╨╕
	ar.escalationsMu.RLock()
	escalation := ar.escalations[chatKey]
	ar.escalationsMu.RUnlock()

	if escalation != nil && escalation.ReturnedAt == nil {
		return nil, nil
	}

	// ╨₧╨┐╤Ç╨╡╨┤╨╡╨╗╤Å╨╡╨╝ ╨░╨▓╤é╨╛╤Ç╨╕╨╖╨░╤å╨╕╤Ä
	userID := ar.getUserIDWithCache(ctx, chat)
	isAuthorized := userID > 0
	log.Printf("[ADK_V2] User context: userID=%d, authorized=%v", userID, isAuthorized)

	// ╨ƒ╨╛╨╗╤â╤ç╨░╨╡╨╝ ╨░╨│╨╡╨╜╤é╨░ (╤ü ╨┐╨╛╨╗╨╜╤ï╨╝ ╨╜╨░╨▒╨╛╤Ç╨╛╨╝ ╨╕╨╖ 19 tools)
	agent, err := ar.getOrCreateAgent(ctx, isAuthorized)
	if err != nil {
		log.Printf("[ADK_V2] ╨₧╤ê╨╕╨▒╨║╨░ ╤ü╨╛╨╖╨┤╨░╨╜╨╕╤Å ╨░╨│╨╡╨╜╤é╨░: %v", err)
		return nil, err
	}

	// ╨ù╨░╨┤╨╡╤Ç╨╢╨║╨░
	if ar.config.DelaySeconds > 0 {
		time.Sleep(time.Duration(ar.config.DelaySeconds) * time.Second)
	}

	// ╨ƒ╨╛╨╗╤â╤ç╨░╨╡╨╝ ╤Å╨╖╤ï╨║ ╨║╨╗╨╕╨╡╨╜╤é╨░ ╨╕╨╖ ╨╝╨╡╤é╨░╨┤╨░╨╜╨╜╤ï╤à ╤ü╨╛╨╛╨▒╤ë╨╡╨╜╨╕╤Å (╨╡╤ü╨╗╨╕ ╨╡╤ü╤é╤î)
	var clientLang string
	if msg.Metadata != nil {
		if detectedLang, ok := msg.Metadata["detectedLanguage"].(string); ok && detectedLang != "" {
			clientLang = detectedLang
			log.Printf("[ADK_V2] Client language detected from metadata: %s", clientLang)
		}
	}

	// ╨ù╨░╨┐╤â╤ü╨║ ╨░╨│╨╡╨╜╤é╨░
	genCtx, cancel := context.WithTimeout(ctx, time.Duration(ar.config.IdleTimeMinutes)*time.Minute)
	defer cancel()

	// ╨ƒ╨╡╤Ç╨╡╨┤╨░╤æ╨╝ ╤Å╨╖╤ï╨║ ╨║╨╗╨╕╨╡╨╜╤é╨░ ╨▓ ╨░╨│╨╡╨╜╤é╨░ (╨╡╤ü╨╗╨╕ ╨╛╨┐╤Ç╨╡╨┤╨╡╨╗╤æ╨╜)
	var response string
	if clientLang != "" {
		response, err = agent.ProcessMessage(genCtx, chatKey, msg.Content, userID, clientLang)
	} else {
		response, err = agent.ProcessMessage(genCtx, chatKey, msg.Content, userID)
	}
	if err != nil {
		log.Printf("[ADK_V2] ╨₧╤ê╨╕╨▒╨║╨░ ╨░╨│╨╡╨╜╤é╨░: %v", err)
		return nil, err
	}

	// ╨ƒ╤Ç╨╛╨▓╨╡╤Ç╨║╨░ ╤ì╤ü╨║╨░╨╗╨░╤å╨╕╨╕
	if agent.IsEscalationNeeded(response) {
		ar.escalationsMu.Lock()
		ar.escalations[chatKey] = &EscalationState{
			EscalatedAt: time.Now(),
		}
		ar.escalationsMu.Unlock()

		response = strings.ReplaceAll(response, "#escalate", "")
		response = strings.TrimSpace(response)
	}

	// ╨ñ╨╛╤Ç╨╝╨╕╤Ç╤â╨╡╨╝ ╨╛╤é╨▓╨╡╤é
	botMsg := &models.Message{
		ChatID:    chat.ID,
		Content:   response,
		Sender:    "admin",
		SenderID:  uuid.Nil,
		Timestamp: time.Now(),
		Read:      true,
		Type:      "text",
		Metadata: map[string]interface{}{
			"isAutoResponse": true,
			"botName":        ar.config.BotName,
			"provider":       "adk-v2",
		},
	}

	return botMsg, nil
}

// ClearEscalation ╨╛╤ç╨╕╤ë╨░╨╡╤é ╤ì╤ü╨║╨░╨╗╨░╤å╨╕╤Ä
func (ar *ADKAutoResponderV2) ClearEscalation(chatID string) {
	ar.escalationsMu.Lock()
	defer ar.escalationsMu.Unlock()
	delete(ar.escalations, chatID)
}

// getOrCreateAgent ╤ü╨╛╨╖╨┤╨░╤æ╤é ╨╕╨╗╨╕ ╨▓╨╛╨╖╨▓╤Ç╨░╤ë╨░╨╡╤é ╨╖╨░╨║╨╡╤ê╨╕╤Ç╨╛╨▓╨░╨╜╨╜╤ï╨╣ ╨░╨│╨╡╨╜╤é V3
func (ar *ADKAutoResponderV2) getOrCreateAgent(ctx context.Context, isAuthorized bool) (*SupportAgent, error) {
	ar.agentsMu.RLock()
	if isAuthorized && ar.authorizedAgent != nil {
		ar.agentsMu.RUnlock()
		return ar.authorizedAgent, nil
	}
	if !isAuthorized && ar.unauthorizedAgent != nil {
		ar.agentsMu.RUnlock()
		return ar.unauthorizedAgent, nil
	}
	ar.agentsMu.RUnlock()

	ar.agentsMu.Lock()
	defer ar.agentsMu.Unlock()

	// Double-check
	if isAuthorized && ar.authorizedAgent != nil {
		return ar.authorizedAgent, nil
	}
	if !isAuthorized && ar.unauthorizedAgent != nil {
		return ar.unauthorizedAgent, nil
	}

	// ╨í╨╛╨╖╨┤╨░╤æ╨╝ V3 ╨░╨│╨╡╨╜╤é╨░ ╤ü ╨┐╨╛╨╗╨╜╤ï╨╝ ╨╜╨░╨▒╨╛╤Ç╨╛╨╝ ╨╕╨╖ 19 tools
	agent, err := NewSupportAgent(ctx, ar.storeClient, isAuthorized)
	if err != nil {
		return nil, err
	}

	if isAuthorized {
		ar.authorizedAgent = agent
		log.Printf("[ADK_V2] Created AUTHORIZED V3 agent with 19 tools")
	} else {
		ar.unauthorizedAgent = agent
		log.Printf("[ADK_V2] Created UNAUTHORIZED V3 agent with 19 tools")
	}

	return agent, nil
}

// getUserIDWithCache ╨┐╨╛╨╗╤â╤ç╨░╨╡╤é user_id ╨╕╨╖ metadata ╨╕╨╗╨╕ ╤ç╨╡╤Ç╨╡╨╖ Store API
func (ar *ADKAutoResponderV2) getUserIDWithCache(ctx context.Context, chat *models.Chat) int {
	// ╨ƒ╤ï╤é╨░╨╡╨╝╤ü╤Å ╨┐╨╛╨╗╤â╤ç╨╕╤é╤î ╨╕╨╖ cache (metadata)
	if chat.Metadata != nil {
		if userID, ok := chat.Metadata["store_user_id"].(int); ok && userID > 0 {
			return userID
		}
		if userID, ok := chat.Metadata["store_user_id"].(float64); ok && userID > 0 {
			return int(userID)
		}
	}

	// ╨ò╤ü╨╗╨╕ ╨╜╨╡ ╨╖╨░╨║╨╡╤ê╨╕╤Ç╨╛╨▓╨░╨╜╨╛ - ╨┐╨╛╨╗╤â╤ç╨░╨╡╨╝ ╤ç╨╡╤Ç╨╡╨╖ Store API
	userID := llm.ExtractUserIDFromChat(ctx, ar.storeClient, chat)

	// NOTE: ╨¥╨╡ ╨╝╨╛╨┤╨╕╤ä╨╕╤å╨╕╤Ç╤â╨╡╨╝ chat.Metadata ╨╖╨┤╨╡╤ü╤î - ╤ì╤é╨╛ ╨┤╨╛╨╗╨╢╨╜╨╛ ╨┤╨╡╨╗╨░╤é╤î╤ü╤Å ╨╜╨░ ╤â╤Ç╨╛╨▓╨╜╨╡ ╨▓╤ï╤ê╨╡
	// ╨│╨┤╨╡ chat ╤ü╨╛╨╖╨┤╨░╤æ╤é╤ü╤Å/╨╖╨░╨│╤Ç╤â╨╢╨░╨╡╤é╤ü╤Å ╨╕╨╖ ╨æ╨ö, ╤ç╤é╨╛╨▒╤ï ╨╕╨╖╨▒╨╡╨╢╨░╤é╤î race conditions

	return userID
}
