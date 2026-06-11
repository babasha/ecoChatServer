//go:build integration

// Сквозной тест цепочки сообщений против настоящего PostgreSQL, поднятого
// локально через embedded-postgres (бинарь скачивается в кеш пользователя,
// без docker/админ-прав). Запуск:
//
//	go test -tags integration -run TestMessageChain -v ./database
//
// Проверяет ровно те пути, что менялись в недавнем рефакторинге ядра:
//   - AddMessage (CTE: успех / chat not found / chat is archived)         — #5
//   - GetChatByID (JOIN user, last-from-slice при before=="" и пагинация) — #6
//   - персистинг перевода SaveDetectedLanguage/SaveTranslation             — #4
//   - unread-счётчик и markRead через GetChats/MarkSpecificMessagesAsRead
package database_test

import (
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/google/uuid"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/models"
)

const (
	pgPort = 54329
	pgUser = "postgres"
	pgPass = "postgres"
	pgDB   = "postgres"
)

func startPostgres(t *testing.T) func() {
	t.Helper()
	pg := embeddedpostgres.NewDatabase(
		embeddedpostgres.DefaultConfig().
			Username(pgUser).Password(pgPass).Database(pgDB).
			Port(pgPort).
			StartTimeout(90 * time.Second),
	)
	if err := pg.Start(); err != nil {
		t.Fatalf("не удалось поднять embedded postgres: %v", err)
	}
	return func() { _ = pg.Stop() }
}

func applySchema(t *testing.T) {
	t.Helper()
	dsn := "host=localhost port=54329 user=postgres password=postgres dbname=postgres sslmode=disable"
	raw, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open raw conn: %v", err)
	}
	defer raw.Close()

	schema, err := os.ReadFile("../migrations/00_base_schema.sql")
	if err != nil {
		t.Fatalf("читать схему: %v", err)
	}
	// pgx stdlib без аргументов использует simple-protocol → многооператорный
	// скрипт (BEGIN/COMMIT, $$-функции) выполняется одним Exec.
	if _, err := raw.Exec(string(schema)); err != nil {
		t.Fatalf("применить схему: %v", err)
	}
}

func TestMessageChain(t *testing.T) {
	stop := startPostgres(t)
	defer stop()
	applySchema(t)

	// Указываем приложению на нашу БД и инициализируем как при реальном старте.
	os.Setenv("PG_HOST", "localhost")
	os.Setenv("PG_PORT", "54329")
	os.Setenv("PG_USER", pgUser)
	os.Setenv("PG_PASSWORD", pgPass)
	os.Setenv("PG_DATABASE", pgDB)
	os.Setenv("PG_SSL_MODE", "disable")

	if err := database.Init(); err != nil {
		t.Fatalf("database.Init: %v", err)
	}
	defer database.Close()

	adminID := uuid.New()

	// ── 1. Создание чата (создаёт client + user + chat) ─────────────────────
	chat, err := database.GetOrCreateChat(
		"user-123", "Cliente Test", "cliente@test.com",
		"widget", "user-123", "bot-1", "test-api-key", nil,
	)
	if err != nil {
		t.Fatalf("GetOrCreateChat: %v", err)
	}
	if !chat.IsNewChat || chat.ID == uuid.Nil {
		t.Fatalf("ожидался новый чат, получили IsNewChat=%v id=%s", chat.IsNewChat, chat.ID)
	}
	t.Logf("✓ чат создан: id=%s client=%s user=%s", chat.ID, chat.ClientID, chat.User.ID)

	// ── 2. Сообщение пользователя (с detectedLanguage) ──────────────────────
	userMsg, err := database.AddMessage(chat.ID, "Hola, necesito ayuda con mi pedido", "user",
		chat.User.ID, "text", map[string]any{"detectedLanguage": "es"})
	if err != nil {
		t.Fatalf("AddMessage(user): %v", err)
	}
	if userMsg.Read {
		t.Fatalf("новое сообщение пользователя должно быть непрочитанным")
	}
	t.Logf("✓ сообщение пользователя сохранено: id=%s", userMsg.ID)

	// небольшая задержка, чтобы timestamp админского сообщения был строго больше
	time.Sleep(5 * time.Millisecond)

	// ── 3. Ответ админа ─────────────────────────────────────────────────────
	adminMsg, err := database.AddMessage(chat.ID, "Claro, ¿cuál es el número de pedido?", "admin",
		adminID, "text", nil)
	if err != nil {
		t.Fatalf("AddMessage(admin): %v", err)
	}
	t.Logf("✓ ответ админа сохранён: id=%s", adminMsg.ID)

	// ── 4. GetChatByID: порядок ASC, total, LastMessage (#6, before=="") ────
	full, total, err := database.GetChatByID(chat.ID, 25, "")
	if err != nil {
		t.Fatalf("GetChatByID: %v", err)
	}
	if total != 2 || len(full.Messages) != 2 {
		t.Fatalf("ожидалось 2 сообщения, total=%d len=%d", total, len(full.Messages))
	}
	if full.Messages[0].ID != userMsg.ID || full.Messages[1].ID != adminMsg.ID {
		t.Fatalf("неверный порядок ASC: [0]=%s [1]=%s", full.Messages[0].Sender, full.Messages[1].Sender)
	}
	if full.LastMessage == nil || full.LastMessage.ID != adminMsg.ID {
		t.Fatalf("LastMessage должен быть ответом админа, получили %+v", full.LastMessage)
	}
	if full.User.Name != "Cliente Test" {
		t.Fatalf("JOIN пользователя не отработал: name=%q", full.User.Name)
	}
	t.Logf("✓ GetChatByID: 2 сообщения в порядке ASC, last=admin, user подтянут JOIN'ом")

	// ── 5. unread через GetChats = 1 (непрочитанное сообщение пользователя) ──
	chats, _, err := database.GetChats(chat.ClientID, adminID, 1, 20)
	if err != nil {
		t.Fatalf("GetChats: %v", err)
	}
	got := findChat(chats, chat.ID)
	if got == nil {
		t.Fatalf("чат не найден в GetChats")
	}
	if got.UnreadCount != 1 {
		t.Fatalf("ожидался unread=1, получили %d", got.UnreadCount)
	}
	t.Logf("✓ GetChats: unread=1")

	// ── 6. markRead → unread = 0 ────────────────────────────────────────────
	marked, err := database.MarkSpecificMessagesAsRead([]uuid.UUID{userMsg.ID})
	if err != nil {
		t.Fatalf("MarkSpecificMessagesAsRead: %v", err)
	}
	if marked != 1 {
		t.Fatalf("ожидалось marked=1, получили %d", marked)
	}
	chats, _, _ = database.GetChats(chat.ClientID, adminID, 1, 20)
	if got = findChat(chats, chat.ID); got == nil || got.UnreadCount != 0 {
		t.Fatalf("после markRead ожидался unread=0, получили %+v", got)
	}
	t.Logf("✓ markRead: помечено 1, unread=0")

	// ── 7. Персистинг перевода (#4): detectedLanguage + translations ────────
	if err := database.SaveDetectedLanguage(adminMsg.ID, "es"); err != nil {
		t.Fatalf("SaveDetectedLanguage: %v", err)
	}
	if err := database.SaveTranslation(adminMsg.ID, "en", "Sure, what is the order number?"); err != nil {
		t.Fatalf("SaveTranslation: %v", err)
	}
	lang, err := database.GetClientLanguageFromChat(chat.ID)
	if err != nil {
		t.Fatalf("GetClientLanguageFromChat: %v", err)
	}
	if lang != "es" {
		t.Fatalf("ожидался язык клиента 'es', получили %q", lang)
	}
	reloaded, _, err := database.GetChatByID(chat.ID, 25, "")
	if err != nil {
		t.Fatalf("GetChatByID после перевода: %v", err)
	}
	last := reloaded.Messages[len(reloaded.Messages)-1]
	tr, _ := last.Metadata["translations"].(map[string]any)
	if tr == nil || tr["en"] != "Sure, what is the order number?" {
		t.Fatalf("перевод не сохранился в metadata: %+v", last.Metadata)
	}
	t.Logf("✓ перевод персистнут: detectedLanguage=es, translations.en присутствует; язык клиента из истории = es")

	// ── 8. Пагинация в историю (#6, before!="") ─────────────────────────────
	older, _, err := database.GetChatByID(chat.ID, 10, adminMsg.Timestamp.Format(time.RFC3339Nano))
	if err != nil {
		t.Fatalf("GetChatByID(before): %v", err)
	}
	for _, m := range older.Messages {
		if !m.Timestamp.Before(adminMsg.Timestamp) {
			t.Fatalf("before-пагинация вернула не-старое сообщение: %s @ %s", m.ID, m.Timestamp)
		}
	}
	if older.LastMessage == nil || older.LastMessage.ID != adminMsg.ID {
		t.Fatalf("при пагинации LastMessage всё равно должен быть самым новым (admin), got=%+v", older.LastMessage)
	}
	t.Logf("✓ пагинация before: вернулись только старые сообщения, LastMessage = самый новый")

	// ── 9. AddMessage в несуществующий чат → "chat not found" (#5) ──────────
	if _, err := database.AddMessage(uuid.New(), "ghost", "user", uuid.New(), "text", nil); err == nil ||
		!strings.Contains(err.Error(), "not found") {
		t.Fatalf("ожидалась ошибка 'chat not found', получили %v", err)
	}
	t.Logf("✓ AddMessage в несуществующий чат → chat not found")

	// ── 10. AddMessage в архивный чат → "chat is archived" (#5) ─────────────
	if _, err := database.DB.Exec("UPDATE chats SET is_archived=true WHERE id=$1", chat.ID); err != nil {
		t.Fatalf("архивирование чата: %v", err)
	}
	if _, err := database.AddMessage(chat.ID, "после архива", "user", chat.User.ID, "text", nil); err == nil ||
		!strings.Contains(err.Error(), "archived") {
		t.Fatalf("ожидалась ошибка 'chat is archived', получили %v", err)
	}
	t.Logf("✓ AddMessage в архивный чат → chat is archived")

	t.Logf("ВСЯ ЦЕПОЧКА ПРОЙДЕНА УСПЕШНО")
}

func findChat(list []models.ChatResponse, id uuid.UUID) *models.ChatResponse {
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}
