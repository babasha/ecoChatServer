// support_ai.go — ИИ-ответчик в чате поддержки сайта.
//
// Механика, и только она: посетитель написал в чат поддержки → сервер собирает
// историю, зовёт LLM-провайдера и кладёт ответ в чат как сообщение стороны
// поддержки (sender='driver', sender_id = SupportAISenderID, metadata.ai=true).
//
// Ничего про конкретный сайт здесь НЕТ и быть не должно. Это публичная
// библиотека чатов: знания конкретного бизнеса живут на стороне бизнеса.
// Отсюда два способа получить ответ, оба задаются настройками:
//
//  1. SUPPORT_BOT_WEBHOOK_URL — ответ пишет сам интегратор. Мы шлём ему
//     сообщение и историю, он возвращает {reply, handoff, metadata} и волен
//     звать любую модель со своими инструментами и своими данными (каталог,
//     заказы, аккаунты). metadata кладётся в сообщение как есть — что в ней,
//     знают только он и его фронт. Это основной путь для взрослой интеграции.
//  2. SUPPORT_AI_PROMPT — встроенный вызов LLM по промпту из настроек, без
//     инструментов. Хватает для «отвечать на частые вопросы».
//
// Не задано ни то, ни другое — ИИ молчит, чат поддержки работает как обычно.
//
// Провайдер — по роли SUPPORT (llm.RoleSupport): SUPPORT_PROVIDER /
// SUPPORT_MODEL / SUPPORT_BASE_URL / SUPPORT_API_KEY; роль не настроена —
// берётся глобальный провайдер. Подойдёт любой OpenAI-совместимый эндпоинт
// (SUPPORT_PROVIDER=openai + SUPPORT_BASE_URL=<endpoint>/v1).
//
// Выключатель — SUPPORT_AI_ENABLED (по умолчанию false).
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/database/queries"
	"github.com/egor/ecochatserver/llm"
	"github.com/egor/ecochatserver/models"
	websocketpkg "github.com/egor/ecochatserver/websocket"
)

// SupportAISenderID — постоянный sender_id ответов ИИ. Детерминирован, поэтому
// «кто ответил — человек или ИИ» читается прямо из messages.sender_id.
var SupportAISenderID = queries.MoradaUserUUID("support_ai", 0)

// supportAIHandoffTag — служебная метка, которой модель просит передать
// разговор живому сотруднику. Что считать поводом для передачи — дело промпта;
// сервер только вырезает метку и помечает сообщение.
const supportAIHandoffTag = "[[HANDOFF]]"

// ---------------------------------------------------------------------------
// Настройки
// ---------------------------------------------------------------------------

type supportAIConfig struct {
	prompt         string
	handoffText    string
	maxTokens      int
	historyLimit   int
	handoffMinutes int
	timeoutSec     int
}

func supportAILoadConfig() supportAIConfig {
	return supportAIConfig{
		prompt:      strings.TrimSpace(database.GetSetting("SUPPORT_AI_PROMPT", "")),
		handoffText: strings.TrimSpace(database.GetSetting("SUPPORT_AI_HANDOFF_TEXT", "")),
		// 1000, а не 200-300: у reasoning-моделей внутренние рассуждения тратят
		// тот же бюджет, что и ответ — при 400 модель упирается в лимит и
		// возвращает ПУСТОЙ content.
		maxTokens:      database.GetSettingInt("SUPPORT_AI_MAX_TOKENS", 1000),
		historyLimit:   database.GetSettingInt("SUPPORT_AI_HISTORY", 16),
		handoffMinutes: database.GetSettingInt("SUPPORT_AI_HANDOFF_MINUTES", 30),
		timeoutSec:     database.GetSettingInt("SUPPORT_AI_TIMEOUT", 90),
	}
}

// ---------------------------------------------------------------------------
// Провайдер: создаётся лениво и переживает смену настроек (hot-swap)
// ---------------------------------------------------------------------------

var (
	supportAIMu          sync.Mutex
	supportAIProviderVal llm.Provider
	supportAIFingerprint string
)

// supportAIFingerprintNow — снимок настроек роли. Изменился снимок — провайдер
// пересоздаётся, поэтому смена модели/ключа в app_settings подхватывается сама
// (с точностью до 5-минутного кеша настроек), без рестарта сервиса.
func supportAIFingerprintNow() string {
	return strings.Join([]string{
		database.GetSetting("SUPPORT_PROVIDER", ""),
		database.GetSetting("SUPPORT_MODEL", ""),
		database.GetSetting("SUPPORT_BASE_URL", ""),
		database.GetSetting("SUPPORT_API_KEY", ""),
		database.GetSetting("LLM_PROVIDER", ""),
		database.GetSetting("OPENAI_MODEL", ""),
		database.GetSetting("OPENAI_BASE_URL", ""),
	}, "|")
}

func supportAIProvider() llm.Provider {
	fp := supportAIFingerprintNow()

	supportAIMu.Lock()
	defer supportAIMu.Unlock()

	if supportAIProviderVal != nil && supportAIFingerprint == fp {
		return supportAIProviderVal
	}

	provider, err := llm.NewProviderForRole(llm.RoleSupport)
	if err != nil {
		log.Printf("[SUPPORT_AI] провайдер недоступен: %v", err)
		supportAIProviderVal = nil
		supportAIFingerprint = fp
		return nil
	}

	supportAIProviderVal = provider
	supportAIFingerprint = fp
	log.Printf("[SUPPORT_AI] провайдер готов: %s", provider.GetName())
	return provider
}

// ---------------------------------------------------------------------------
// Лимиты: защита от зацикливания и от неожиданного счёта у провайдера
// ---------------------------------------------------------------------------

var (
	supportAILimitMu sync.Mutex
	supportAIPerChat = map[uuid.UUID][]time.Time{}
	supportAIDayCnt  int
	supportAIDay     string
)

// supportAITakeSlot списывает один ответ из часового лимита чата и суточного
// лимита сервера. Счётчики в памяти: рестарт их обнуляет — это осознанно,
// лимиты здесь страхуют от разгона, а не ведут бухгалтерию.
func supportAITakeSlot(chatID uuid.UUID) bool {
	perChat := database.GetSettingInt("SUPPORT_AI_MAX_PER_CHAT_HOUR", 20)
	perDay := database.GetSettingInt("SUPPORT_AI_MAX_PER_DAY", 300)

	now := time.Now()
	today := now.Format("2006-01-02")

	supportAILimitMu.Lock()
	defer supportAILimitMu.Unlock()

	if supportAIDay != today {
		supportAIDay = today
		supportAIDayCnt = 0
		supportAIPerChat = map[uuid.UUID][]time.Time{}
	}
	if perDay > 0 && supportAIDayCnt >= perDay {
		log.Printf("[SUPPORT_AI] суточный лимит %d исчерпан, чат %s без ответа ИИ", perDay, chatID)
		return false
	}

	kept := supportAIPerChat[chatID][:0]
	for _, t := range supportAIPerChat[chatID] {
		if now.Sub(t) < time.Hour {
			kept = append(kept, t)
		}
	}
	if perChat > 0 && len(kept) >= perChat {
		supportAIPerChat[chatID] = kept
		log.Printf("[SUPPORT_AI] часовой лимит %d исчерпан для чата %s", perChat, chatID)
		return false
	}

	supportAIPerChat[chatID] = append(kept, now)
	supportAIDayCnt++
	return true
}

// ---------------------------------------------------------------------------
// Основной ход
// ---------------------------------------------------------------------------

// maybeRunSupportAI решает, должен ли ИИ ответить на сообщение посетителя, и
// отвечает. Вызывается в отдельной горутине из processSendMessage: ни ReadPump,
// ни доставка сообщения посетителя её не ждут.
//
// Один ход на чат одновременно. Люди пишут очередями («oi» / «procuro apê» /
// «no centro»), и без этой очереди каждое сообщение запускало свой ход
// параллельно: три оплаченных ответа внахлёст, и каждый не видит, что уже
// показали остальные. Пока ход идёт, новые сообщения только запоминаются
// (последнее вытесняет предыдущее); по окончании хода отвечаем ОДИН раз на
// последнее — предыдущие оно увидит в истории.
func maybeRunSupportAI(chatID uuid.UUID, userMsg *models.Message) {
	if userMsg == nil || userMsg.Sender != "user" {
		return
	}

	supportAIBusyMu.Lock()
	if _, busy := supportAIBusy[chatID]; busy {
		supportAIBusy[chatID] = userMsg
		supportAIBusyMu.Unlock()
		return
	}
	supportAIBusy[chatID] = nil
	supportAIBusyMu.Unlock()

	// Паника в ходе не должна навсегда оставить чат «занятым».
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[SUPPORT_AI] паника в ходе чата %s: %v", chatID, r)
			supportAIBusyMu.Lock()
			delete(supportAIBusy, chatID)
			supportAIBusyMu.Unlock()
		}
	}()

	for {
		runSupportAI(chatID, userMsg)

		supportAIBusyMu.Lock()
		next := supportAIBusy[chatID]
		if next == nil {
			delete(supportAIBusy, chatID)
			supportAIBusyMu.Unlock()
			return
		}
		supportAIBusy[chatID] = nil
		supportAIBusyMu.Unlock()
		userMsg = next
	}
}

var (
	supportAIBusyMu sync.Mutex
	// Ключ есть — ход идёт; значение — последнее сообщение, пришедшее за это время.
	supportAIBusy = map[uuid.UUID]*models.Message{}
)

func runSupportAI(chatID uuid.UUID, userMsg *models.Message) {
	if !database.GetSettingBool("SUPPORT_AI_ENABLED", false) {
		return
	}
	// Картинки и файлы ИИ не читает — такие сообщения оставляем людям.
	if userMsg.Type != "" && userMsg.Type != "text" {
		return
	}
	if strings.TrimSpace(userMsg.Content) == "" {
		return
	}

	cfg := supportAILoadConfig()
	// Два способа получить ответ, и оба опциональны:
	//   1) вебхук — отвечает сам интегратор (он знает свой бизнес и свои данные);
	//   2) встроенный вызов LLM по промпту из настроек.
	// Не настроено ни одного — чат поддержки работает как раньше.
	webhookURL := strings.TrimSpace(database.GetSetting("SUPPORT_BOT_WEBHOOK_URL", ""))
	if webhookURL == "" && cfg.prompt == "" {
		log.Printf("[SUPPORT_AI] ни SUPPORT_BOT_WEBHOOK_URL, ни SUPPORT_AI_PROMPT не заданы — ИИ не отвечает")
		return
	}

	isSupport, err := database.IsMoradaSupportChat(chatID)
	if err != nil {
		log.Printf("[SUPPORT_AI] IsMoradaSupportChat(%s): %v", chatID, err)
		return
	}
	if !isSupport {
		return
	}

	chat, _, err := database.GetChatByID(chatID, cfg.historyLimit, "")
	if err != nil {
		log.Printf("[SUPPORT_AI] история чата %s: %v", chatID, err)
		return
	}

	// Человек уже в разговоре — ИИ молчит. Окно отсчитывается от последнего
	// ответа живого сотрудника; новое сообщение посетителя его не сбрасывает.
	if humanRepliedRecently(chat.Messages, time.Duration(cfg.handoffMinutes)*time.Minute) {
		log.Printf("[SUPPORT_AI] чат %s ведёт человек — пропускаем", chatID)
		return
	}

	// Провайдер нужен только встроенному пути; при вебхуке модель зовёт
	// интегратор, и ключа у нас может не быть вовсе.
	var provider llm.Provider
	if webhookURL == "" {
		provider = supportAIProvider()
		if provider == nil {
			return
		}
	}
	if !supportAITakeSlot(chatID) {
		return
	}

	history := supportAIHistory(chat.Messages, userMsg.ID)

	// «Печатает…» — чтобы пауза на генерацию не выглядела как молчание.
	if WebSocketHub != nil {
		if typing, terr := websocketpkg.NewTypingMessage(chatID, true, "driver"); terr == nil {
			WebSocketHub.SendToChat(chatID.String(), typing)
		}
	}
	defer func() {
		if WebSocketHub != nil {
			if typing, terr := websocketpkg.NewTypingMessage(chatID, false, "driver"); terr == nil {
				WebSocketHub.SendToChat(chatID.String(), typing)
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.timeoutSec)*time.Second)
	defer cancel()

	var (
		text     string
		escalate bool
		extra    map[string]any
	)
	if webhookURL != "" {
		text, escalate, extra, err = supportAIAskWebhook(ctx, webhookURL, chat, chatID, userMsg,
			supportAIWebhookHistory(chat.Messages, userMsg.ID))
		if err != nil {
			log.Printf("[SUPPORT_AI] вебхук %s для чата %s: %v", webhookURL, chatID, err)
			return
		}
	} else {
		clientID := chat.ClientID
		resp, gerr := provider.GenerateResponse(ctx, userMsg.Content, history, &llm.GenerateOptions{
			Temperature:  0.3,
			MaxTokens:    cfg.maxTokens,
			SystemPrompt: supportAIPromptWithContext(cfg.prompt, chat),
			ClientID:     &clientID,
			ChatID:       &chatID,
		})
		if gerr != nil {
			log.Printf("[SUPPORT_AI] генерация для чата %s: %v", chatID, gerr)
			return
		}
		text, escalate = splitSupportAIHandoff(resp.Text)
	}

	// Стандартную фразу «зову человека» дописываем ТОЛЬКО встроенному пути.
	// У интегратора формулировка своя — он вернул готовый текст, и приписка
	// к нему была бы вторым «сейчас позову коллегу» подряд.
	if escalate && webhookURL == "" {
		if text == "" {
			text = cfg.handoffText
		} else if cfg.handoffText != "" {
			text += "\n\n" + cfg.handoffText
		}
	}
	// Пустой текст — это «промолчать», КРОМЕ случая, когда интегратор прислал
	// вложение (карточки и т.п.): такое сообщение содержательно само по себе.
	if strings.TrimSpace(text) == "" && len(extra) == 0 {
		log.Printf("[SUPPORT_AI] пустой ответ для чата %s", chatID)
		return
	}

	meta := map[string]any{"ai": true}
	if escalate {
		meta["aiHandoff"] = true
	}
	// Всё, что прислал интегратор, кладём в metadata как есть: ecoChat не знает
	// и не должна знать, что там внутри — это данные его предметной области.
	for k, v := range extra {
		if k == "ai" || k == "aiHandoff" {
			continue
		}
		meta[k] = v
	}

	saved, err := database.AddMessage(chatID, text, "driver", SupportAISenderID, "text", meta)
	if err != nil {
		log.Printf("[SUPPORT_AI] сохранение ответа в чат %s: %v", chatID, err)
		return
	}

	deliverMoradaMessage(chatID, saved)
	log.Printf("[SUPPORT_AI] ответ отправлен в чат %s (handoff=%v, %d симв.)", chatID, escalate, len(text))
}

// ---------------------------------------------------------------------------
// Вебхук: ответ пишет интегратор
// ---------------------------------------------------------------------------

// supportAIAskWebhook спрашивает ответ у стороннего сервиса. Это основной путь
// для интеграторов, у которых есть СВОИ данные (каталог, заказы, аккаунты):
// они сами зовут модель, сами дают ей инструменты и сами хранят ключ, а ecoChat
// остаётся транспортом.
//
// Запрос:  {chatId, visitor:{id,name}, message:{content,type}, history:[{role,content}]}
// Ответ:   {reply: "текст", handoff: bool, metadata: {…}}
// Пустой reply без metadata = «промолчать», это нормальный ответ, не ошибка.
func supportAIAskWebhook(
	ctx context.Context,
	url string,
	chat *models.Chat,
	chatID uuid.UUID,
	userMsg *models.Message,
	history []supportAIHistoryItem,
) (string, bool, map[string]any, error) {

	visitor := map[string]any{"name": chat.User.Name}
	// client_id_ext — идентификатор посетителя в системе интегратора. Без него
	// вебхук может только отвечать, но не действовать от имени человека.
	if chat.ClientIDExt != nil {
		visitor["id"] = *chat.ClientIDExt
	}

	body, err := json.Marshal(map[string]any{
		"chatId":  chatID.String(),
		"visitor": visitor,
		"message": map[string]any{"content": userMsg.Content, "type": userMsg.Type},
		"history": history,
	})
	if err != nil {
		return "", false, nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", false, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	// Имя заголовка и секрет задаёт интегратор: ecoChat только передаёт их.
	if name := strings.TrimSpace(database.GetSetting("SUPPORT_BOT_WEBHOOK_HEADER", "")); name != "" {
		req.Header.Set(name, database.GetSetting("SUPPORT_BOT_WEBHOOK_SECRET", ""))
	}

	resp, err := supportAIWebhookClient().Do(req)
	if err != nil {
		return "", false, nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return "", false, nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", false, nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var out struct {
		Reply    string         `json:"reply"`
		Handoff  bool           `json:"handoff"`
		Metadata map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", false, nil, fmt.Errorf("ответ не JSON: %w", err)
	}
	return strings.TrimSpace(out.Reply), out.Handoff, out.Metadata, nil
}

// supportAIWebhookClient — клиент с таймаутом ПОД ответ интегратора: он у себя
// может ходить в модель с инструментами, и это дольше обычного HTTP-вызова.
func supportAIWebhookClient() *http.Client {
	return &http.Client{
		Timeout: time.Duration(database.GetSettingInt("SUPPORT_BOT_WEBHOOK_TIMEOUT", 90)) * time.Second,
	}
}

// humanRepliedRecently — отвечал ли живой сотрудник поддержки за последние
// `within`. Ответы самого ИИ (sender_id = SupportAISenderID) не считаются.
func humanRepliedRecently(messages []models.Message, within time.Duration) bool {
	if within <= 0 {
		return false
	}
	cutoff := time.Now().Add(-within)
	for _, m := range messages {
		if m.Sender == "user" || m.SenderID == SupportAISenderID {
			continue
		}
		if m.Timestamp.After(cutoff) {
			return true
		}
	}
	return false
}

// supportAIHistory переводит историю чата в диалог для модели. Последнее
// сообщение посетителя исключается: оно уходит отдельным аргументом
// GenerateResponse, иначе модель увидит его дважды.
func supportAIHistory(messages []models.Message, skipID uuid.UUID) []llm.Message {
	out := make([]llm.Message, 0, len(messages))
	for _, m := range messages {
		if m.ID == skipID {
			continue
		}
		content := strings.TrimSpace(m.Content)
		if m.Type == "image" {
			content = "[image]"
		}
		if content == "" {
			continue
		}
		role := "assistant"
		if m.Sender == "user" {
			role = "user"
		}
		out = append(out, llm.Message{Role: role, Content: content})
	}
	return out
}

// supportAIPromptWithContext добавляет к промпту то, что сервер знает о
// собеседнике: имя посетителя и дату (модель её не знает). Заголовок блока —
// настройка SUPPORT_AI_CONTEXT_HEADER: язык выбирает интегратор, сервер его не
// навязывает.
func supportAIPromptWithContext(prompt string, chat *models.Chat) string {
	var b strings.Builder
	b.WriteString(prompt)
	b.WriteString("\n\n")
	b.WriteString(database.GetSetting("SUPPORT_AI_CONTEXT_HEADER", "CONTEXT"))
	b.WriteString("\n- date: " + time.Now().Format("2006-01-02") + "\n")
	if name := strings.TrimSpace(chat.User.Name); name != "" {
		b.WriteString("- visitor name: " + name + "\n")
	}
	return b.String()
}

// splitSupportAIHandoff вырезает служебную метку передачи человеку и
// возвращает очищенный текст плюс признак эскалации.
func splitSupportAIHandoff(raw string) (string, bool) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", false
	}
	escalate := strings.Contains(text, supportAIHandoffTag)
	if escalate {
		text = strings.ReplaceAll(text, supportAIHandoffTag, "")
	}
	return strings.TrimSpace(text), escalate
}

// supportAIHistoryItem — сообщение истории в том виде, в каком его получает
// вебхук. metadata передаётся как есть: интегратор клал туда свои данные
// (например, что именно он уже показал в этом чате) и без них его следующий
// ответ будет слепым — он увидит только текст своих же прошлых сообщений.
type supportAIHistoryItem struct {
	Role     string         `json:"role"`
	Content  string         `json:"content"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// supportAIWebhookHistory — та же история, что и для встроенного пути, но с
// метаданными и без последнего сообщения посетителя (оно едет отдельно).
func supportAIWebhookHistory(messages []models.Message, skipID uuid.UUID) []supportAIHistoryItem {
	out := make([]supportAIHistoryItem, 0, len(messages))
	for _, m := range messages {
		if m.ID == skipID {
			continue
		}
		content := strings.TrimSpace(m.Content)
		if m.Type == "image" {
			content = "[image]"
		}
		role := "assistant"
		if m.Sender == "user" {
			role = "user"
		}
		if content == "" && len(m.Metadata) == 0 {
			continue
		}
		out = append(out, supportAIHistoryItem{Role: role, Content: content, Metadata: m.Metadata})
	}
	return out
}
