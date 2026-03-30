//go:build ignore

// seed_chats.go — generates realistic chat data for testing Director memory & search.
// Usage: go run scripts/seed_chats.go

package main

import (
	"crypto/md5"
	"database/sql"
	"fmt"
	"log"
	"math/rand"
	"time"

	_ "github.com/lib/pq"
)

const dsn = "postgres://ecouser@localhost:5432/ecochat?sslmode=disable"

var userNames = []string{
	"Алексей Петров", "Мария Иванова", "Дмитрий Сидоров", "Анна Козлова",
	"Сергей Новиков", "Елена Морозова", "Андрей Волков", "Ольга Лебедева",
	"Николай Соколов", "Татьяна Попова", "Игорь Зайцев", "Наталья Смирнова",
	"Владимир Орлов", "Светлана Кузнецова", "Михаил Федоров", "Юлия Васильева",
	"Павел Михайлов", "Ирина Новикова", "Артем Семенов", "Ксения Голубева",
	"Роман Степанов", "Вера Борисова", "Станислав Громов", "Людмила Фролова",
	"Евгений Тихонов", "Галина Медведева", "Константин Ершов", "Зинаида Чернова",
	"Борис Никитин", "Раиса Осипова", "Леонид Матвеев", "Нина Захарова",
	"Валерий Виноградов", "Тамара Соловьева", "Геннадий Титов", "Лидия Кудрявцева",
	"Вячеслав Кузьмин", "Антонина Ларионова", "Анатолий Ильин", "Валентина Гусева",
	"Степан Романов", "Надежда Сорокина", "Феликс Богданов", "Клавдия Ковалева",
	"Иван Беляев", "Зоя Дмитриева", "Глеб Егоров", "Полина Яковлева",
	"Тимур Алексеев", "Дарья Максимова",
}

var deviceModels = []string{
	"Zefir Pro X3", "Zefir Lite S5", "Zefir Ultra 2", "Zefir Mini M1",
	"EcoSensor V2", "EcoSensor Pro", "EcoHub 3000", "EcoHub Mini",
}
var firmwareVersions = []string{"1.2", "1.3", "1.4", "2.0", "2.1", "2.1.1", "2.2-beta"}
var sources = []string{"web", "mobile", "web_widget", "mobile", "web"}
var statuses = []string{"closed", "closed", "closed", "open", "active"}

type MsgPair struct{ User, Agent string }
type ChatTemplate struct {
	Topic    string
	Messages []MsgPair
}

var chatTemplates = []ChatTemplate{
	{"firmware_crash", []MsgPair{
		{"Устройство %s постоянно перезагружается после обновления прошивки %s", "Понял вас. Это известная проблема с прошивкой %s. Рекомендую откатиться на версию 1.2."},
		{"Как откатиться?", "Зайдите в Настройки → О устройстве → Откатить. Или скачайте файл 1.2.bin с сайта."},
		{"Попробовал, не помогает", "Тогда сделайте сброс до заводских настроек. Сохраните конфигурацию заранее."},
		{"Спасибо, помогло!", "Отлично! Мы передали информацию команде разработки. Проблема будет исправлена в следующем обновлении."},
	}},
	{"connectivity_wifi", []MsgPair{
		{"%s не подключается к WiFi 6", "%s поддерживает WiFi 5 (802.11ac). WiFi 6 в режиме совместимости нестабилен."},
		{"Что делать?", "Попробуйте: 1) Перезагрузить роутер 2) Переключить на 2.4 GHz 3) Отключить WPA3"},
		{"2.4 GHz помогло!", "Отлично! В прошивке 2.2 добавлена поддержка WiFi 6E, обновитесь когда выйдет."},
	}},
	{"sensor_offline", []MsgPair{
		{"Датчик %s показывает offline уже 3 часа", "Проверьте питание. Жёлтый индикатор — проблема с сетью, красный — нет питания."},
		{"Индикатор жёлтый", "Устройство включено, но нет связи. Зажмите кнопку reset на 5 секунд."},
		{"Перезагрузил, мигает синим", "Синий мигающий — режим сопряжения. Откройте приложение → Добавить устройство."},
		{"Подключилось! Спасибо", "Отлично! Принудительный ресет — стандартное решение при потере связи."},
	}},
	{"billing_question", []MsgPair{
		{"Когда списывается оплата за подписку?", "Оплата списывается в день активации подписки каждый месяц."},
		{"У меня списали два раза", "Приношу извинения! Уточните дату и сумму, я подниму тикет на возврат."},
		{"15 числа 590р и 16 числа ещё 590р", "Вижу проблему. Создал заявку на возврат 590р. Деньги вернутся в течение 5 рабочих дней."},
		{"Хорошо, жду", "Как только возврат обработается — придёт уведомление на email."},
	}},
	{"setup_new_device", []MsgPair{
		{"Купил %s, не могу настроить", "Рад помочь! iOS или Android? Приложение уже установлено?"},
		{"Android, приложение есть", "Откройте приложение → нажмите + → Добавить устройство → %s → Следуйте инструкции."},
		{"Приложение не видит устройство по Bluetooth", "Убедитесь что Bluetooth включён. Зажмите кнопку на устройстве 5 сек — синий мигающий индикатор."},
		{"Нашло! Настраиваю", "Отлично! Если нужна помощь с автоматизациями — спрашивайте."},
	}},
	{"app_crash_ios", []MsgPair{
		{"Приложение на iOS 17 вылетает при открытии графиков", "Это баг в версии 3.2.1 на iOS 17. Патч выходит в пятницу."},
		{"Есть временное решение?", "Да: Настройки → Отображение → Отключить анимацию графиков."},
		{"Помогло, спасибо!", "Пожалуйста! Не забудьте обновить приложение в пятницу."},
	}},
	{"escalation_angry", []MsgPair{
		{"Это ужасно! %s выходит из строя второй раз за месяц!", "Очень жаль. Два отказа за месяц — неприемлемо. Оформляю замену устройства."},
		{"Хочу возврат денег", "Понимаю. Оформляю замену + 3 месяца подписки бесплатно в качестве компенсации."},
		{"Ладно, но это последний раз", "Обещаем. Новое устройство отправим завтра курьером. Удачи!"},
	}},
	{"data_export", []MsgPair{
		{"Как выгрузить данные с %s за год?", "Личный кабинет → Устройства → Экспорт данных → Выбрать период → CSV."},
		{"Не вижу кнопку экспорта", "Экспорт доступен только на тарифе Pro и выше. Ваш тариф — Basic?"},
		{"Да, Basic", "На Basic — только последние 30 дней. Для полного года нужен тариф Pro (590р/мес)."},
		{"Обновлю тариф", "После обновления данные доступны сразу."},
	}},
	{"api_integration", []MsgPair{
		{"Как подключить %s к Home Assistant?", "Есть официальная интеграция через MQTT. Добавьте broker в configuration.yaml."},
		{"Топик для данных?", "Топик: eco/%s/sensor/state. Формат: {value: 23.5, unit: °C, ts: unix}"},
		{"Задержка 5 секунд", "5 сек — через облако. Для локального MQTT задержка меньше 100ms."},
		{"Включил локальный, мгновенно!", "Отлично! Для этого нужна одна сеть с Home Assistant."},
	}},
	{"battery_drain", []MsgPair{
		{"%s разряжается за 2 дня вместо 30", "Ненормальный разряд. Как часто опрос датчиков настроен?"},
		{"Каждые 10 секунд", "Рекомендую 60 секунд. Также отключите постоянный Bluetooth если не нужен."},
		{"Поменял на 60 сек, посмотрю", "Отлично. Обновитесь до прошивки 2.1 — там лучше оптимизация питания."},
		{"Стало лучше, 10 дней держит", "Намного лучше! Для максимума — интервал 120 сек, WiFi только при отправке данных."},
	}},
	{"warranty_claim", []MsgPair{
		{"%s сломался после 8 месяцев использования, гарантия ещё действует?", "Гарантия 12 месяцев с даты покупки. Проверьте чек — если покупка была меньше года назад, замена бесплатно."},
		{"Покупал 7 месяцев назад", "Тогда вы в гарантийном периоде. Опишите симптомы поломки."},
		{"Не включается, индикатор не горит", "Классическая неисправность блока питания. Оформляю гарантийную замену. Адрес для отправки?"},
		{"Спасибо, жду замену", "Замена придёт в течение 3-5 рабочих дней. Трек-номер пришлём на email."},
	}},
	{"firmware_update_failed", []MsgPair{
		{"Обновление прошивки %s завершилось ошибкой на 80%", "Устройство могло зависнуть в bootloader. Попробуйте зажать reset + power одновременно на 10 секунд."},
		{"Зажал, устройство перезагрузилось", "Хорошо. Теперь принудительно обновите через USB: скачайте файл с сайта и запустите flash_tool."},
		{"Обновление через USB прошло успешно", "Отлично! Версия обновилась. Убедитесь что отображается нужная версия в разделе О устройстве."},
	}},
}

func hash32(s string) string {
	h := md5.Sum([]byte(s))
	return fmt.Sprintf("%x", h)[:32]
}

func main() {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatal("DB open:", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		log.Fatal("DB ping:", err)
	}
	log.Printf("Connected to ecochat DB")

	clientID := "11111111-1111-1111-1111-111111111111"
	agentID := "00000000-0000-0000-0000-000000000001"
	rng := rand.New(rand.NewSource(42))
	now := time.Now()
	start := now.AddDate(0, 0, -30)

	// Create 50 test users
	userIDs := make([]string, len(userNames))
	log.Printf("Creating %d users...", len(userNames))
	for i, name := range userNames {
		var uid string
		sourceID := fmt.Sprintf("seed_%d", i+1)
		db.QueryRow(`SELECT id FROM users WHERE source='web' AND source_id=$1`, sourceID).Scan(&uid)
		if uid == "" {
			db.QueryRow(`
				INSERT INTO users (name, email, source, source_id, profile_url)
				VALUES ($1, $2, 'web', $3, '') RETURNING id`,
				name, fmt.Sprintf("user%d@ecotest.local", i+1), sourceID,
			).Scan(&uid)
		}
		if uid == "" {
			log.Fatalf("Could not create user %s", name)
		}
		userIDs[i] = uid
	}
	log.Printf("Users ready: %d", len(userIDs))

	// Generate 500 chats
	totalChats := 500
	chatsMade, msgsMade := 0, 0
	log.Printf("Generating %d chats over 30 days...", totalChats)

	for i := 0; i < totalChats; i++ {
		offsetSec := rng.Int63n(int64(30 * 24 * 3600))
		chatTime := start.Add(time.Duration(offsetSec) * time.Second)

		userID := userIDs[rng.Intn(len(userIDs))]
		source := sources[rng.Intn(len(sources))]
		status := statuses[rng.Intn(len(statuses))]
		tmpl := chatTemplates[rng.Intn(len(chatTemplates))]
		device := deviceModels[rng.Intn(len(deviceModels))]
		firmware := firmwareVersions[rng.Intn(len(firmwareVersions))]

		var chatID string
		err := db.QueryRow(`
			INSERT INTO chats (user_id, created_at, updated_at, status, source, bot_id, client_id, auto_responder_enabled)
			VALUES ($1, $2, $2, $3::chat_status, $4::chat_source, 'eco_bot', $5, true)
			RETURNING id`,
			userID, chatTime, status, source, clientID,
		).Scan(&chatID)
		if err != nil {
			log.Printf("Chat %d error: %v", i, err)
			continue
		}
		chatsMade++

		msgTime := chatTime
		for _, pair := range tmpl.Messages {
			// Format with device/firmware (safe: use %s substitution with fixed args)
			uMsg := fmt.Sprintf(pair.User, device, firmware)
			aMsg := fmt.Sprintf(pair.Agent, device, firmware)

			// User message
			msgTime = msgTime.Add(time.Duration(30+rng.Intn(180)) * time.Second)
			uHash := hash32(fmt.Sprintf("u%s%s%s", chatID, userID, uMsg))
			db.Exec(`
				INSERT INTO messages (chat_id, content, sender, sender_id, timestamp, type, source, content_hash)
				VALUES ($1, $2, 'user', $3, $4, 'text', $5, $6) ON CONFLICT DO NOTHING`,
				chatID, uMsg, userID, msgTime, source, uHash)

			// Agent reply
			msgTime = msgTime.Add(time.Duration(3+rng.Intn(15)) * time.Second)
			aHash := hash32(fmt.Sprintf("a%s%s%s", chatID, agentID, aMsg))
			db.Exec(`
				INSERT INTO messages (chat_id, content, sender, sender_id, timestamp, type, source, content_hash)
				VALUES ($1, $2, 'admin', $3, $4, 'text', $5, $6) ON CONFLICT DO NOTHING`,
				chatID, aMsg, agentID, msgTime, source, aHash)
			msgsMade += 2
		}

		if (i+1)%100 == 0 {
			log.Printf("  %d/%d chats, %d messages", i+1, totalChats, msgsMade)
		}
	}

	// Final stats
	var chatCount, msgCount int
	db.QueryRow("SELECT COUNT(*) FROM chats WHERE bot_id='eco_bot'").Scan(&chatCount)
	db.QueryRow(`SELECT COUNT(*) FROM messages m JOIN chats c ON c.id=m.chat_id WHERE c.bot_id='eco_bot'`).Scan(&msgCount)
	log.Printf("\n✓ Done! DB: %d chats total, %d messages total (inserted this run: %d chats, %d msgs)",
		chatCount, msgCount, chatsMade, msgsMade)
}
