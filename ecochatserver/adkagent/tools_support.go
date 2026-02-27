package adkagent

import (
	"fmt"
	"log"
	"slices"
	"strings"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// ============================================================================
// SUPPORT TOOLS — 5 tools for Zefir general support
// ============================================================================

// CreateSupportTools creates 5 support tools for Zefir
func CreateSupportTools() ([]tool.Tool, error) {
	var tools []tool.Tool

	// Tool 1: search_faq — 49 entries across 8 categories
	t1, err := createSearchFAQTool()
	if err != nil {
		return nil, err
	}
	tools = append(tools, t1)

	// Tool 2: get_app_info
	t2, err := createGetAppInfoTool()
	if err != nil {
		return nil, err
	}
	tools = append(tools, t2)

	// Tool 3: get_contact_info
	t3, err := createGetContactInfoTool()
	if err != nil {
		return nil, err
	}
	tools = append(tools, t3)

	// Tool 4: get_feature_guide
	t4, err := createGetFeatureGuideTool()
	if err != nil {
		return nil, err
	}
	tools = append(tools, t4)

	// Tool 5: get_security_info
	t5, err := createGetSecurityInfoTool()
	if err != nil {
		return nil, err
	}
	tools = append(tools, t5)

	log.Printf("[TOOLS_SUPPORT] Created %d support tools", len(tools))
	return tools, nil
}

// ============================================================================
// FAQ DATABASE — 49 entries across 8 categories
// ============================================================================

type faqEntry struct {
	question string
	answer   string
	category string
	tags     []string
}

var faqDatabase = []faqEntry{
	// GENERAL (7)
	{question: "What is Zefir?", answer: "Open-source IoT plant monitoring: ESP32-C3 sensors measure soil moisture + temp, mesh network, cross-platform app (Tauri+Preact).", category: "general", tags: []string{"zefir", "what", "about", "overview"}},
	{question: "Is Zefir free?", answer: "Open-source (MIT). App + firmware free. Buy/build ESP32-C3 sensor. Cloud free for ≤10 devices.", category: "general", tags: []string{"free", "cost", "price", "open-source"}},
	{question: "What platforms?", answer: "Windows, macOS, Linux (Tauri), iOS, Android. Web: zefir.app.", category: "general", tags: []string{"platform", "ios", "android", "windows", "mac", "linux"}},
	{question: "How many plants?", answer: "1 sensor = 1 plant. Up to 10 per mesh, unlimited per account.", category: "general", tags: []string{"plants", "limit", "how many", "sensors"}},
	{question: "Languages?", answer: "6: Russian, English, German, Spanish, Portuguese, Chinese.", category: "general", tags: []string{"language", "russian", "english", "german", "spanish"}},
	{question: "Need internet?", answer: "For cloud sync yes. Sensors store locally ~7 days, sync when back online. Mesh works offline, only ROOT needs WiFi.", category: "general", tags: []string{"internet", "offline", "wifi", "cloud"}},
	{question: "Outdoor use?", answer: "Indoor only (IP44 splash-proof). Outdoor needs weatherproof enclosure. Probe is water-resistant.", category: "general", tags: []string{"outdoor", "waterproof", "garden", "outside"}},

	// SENSORS (8)
	{question: "What sensor?", answer: "ESP32-C3 + capacitive soil moisture sensor. WiFi + BLE, RISC-V, ultra-low power.", category: "sensors", tags: []string{"sensor", "esp32", "hardware", "chip"}},
	{question: "Accuracy?", answer: "±3% moisture in typical soil. Calibrate for your soil: Device Settings → Calibrate.", category: "sensors", tags: []string{"accuracy", "precision", "moisture", "calibrate"}},
	{question: "Battery life?", answer: "15min=2mo, 30min(default)=4mo, 1hr=6mo. ROOT ~2mo. Cold reduces life.", category: "sensors", tags: []string{"battery", "life", "charge", "power"}},
	{question: "How to charge?", answer: "USB-C. White LED=charging, green=full. ~2 hours. Any 5V charger.", category: "sensors", tags: []string{"charge", "usb", "power", "cable"}},
	{question: "DIY sensor?", answer: "Yes, open-source on GitHub. Need: ESP32-C3 board, capacitive sensor, LiPo battery, 3D case (STL provided).", category: "sensors", tags: []string{"diy", "build", "custom", "open-source", "hardware"}},
	{question: "Reading interval?", answer: "Default 30min. Configurable 15min–6hr in Device Settings. More frequent = more drain.", category: "sensors", tags: []string{"interval", "frequency", "reading", "schedule"}},
	{question: "Air humidity?", answer: "No, measures SOIL only. Air needs DHT22/BME280 add-on (DIY).", category: "sensors", tags: []string{"air", "humidity", "temperature", "soil"}},
	{question: "Light sensor?", answer: "Not included. Optional BH1750 add-on (DIY). App supports it if detected.", category: "sensors", tags: []string{"light", "lux", "sensor", "module"}},

	// SETUP (6)
	{question: "First setup?", answer: "1)Download app 2)Add Device 3)Hold button 3s for BLE 4)WiFi config 5)Assign plant 6)Insert probe.", category: "setup", tags: []string{"setup", "first", "start", "install", "begin"}},
	{question: "BLE won't pair?", answer: "Charge sensor, BT ON, hold 3s→BLUE blink, <1m distance. Android: Location permission. iOS: BLE permission.", category: "setup", tags: []string{"bluetooth", "ble", "pair", "connect", "not found"}},
	{question: "WiFi failed?", answer: "2.4GHz ONLY (no 5GHz). Check password, WPA2/3. Dual-band: disable band steering.", category: "setup", tags: []string{"wifi", "network", "connect", "fail", "password"}},
	{question: "Add second sensor?", answer: "Same process. Auto-joins mesh as NODE. Place within 10m of existing sensor.", category: "setup", tags: []string{"second", "add", "another", "new", "mesh"}},
	{question: "Factory reset?", answer: "Hold button 10s → all LEDs flash. Erases WiFi, mesh, plant config.", category: "setup", tags: []string{"reset", "factory", "default", "erase", "clear"}},
	{question: "Move sensor?", answer: "App: device → Edit → Change Plant. Thresholds auto-update.", category: "setup", tags: []string{"move", "change", "plant", "reassign", "different"}},

	// PLANTS (6)
	{question: "Plant database size?", answer: "89 species in 5 categories: Tropical(25), Succulent(20), Herb(15), Vegetable(14), Flowering(15). Pre-configured thresholds.", category: "plants", tags: []string{"database", "species", "how many", "plants", "list"}},
	{question: "Custom plant?", answer: "Yes: 'Custom Plant' → set name, moisture min/max, temp range, photo. Works like DB plants.", category: "plants", tags: []string{"custom", "add", "new", "plant", "own"}},
	{question: "Moisture thresholds?", answer: "Min/max soil moisture %. Below min=too dry alert, above max=too wet. Pre-configured per species, customizable.", category: "plants", tags: []string{"threshold", "moisture", "min", "max", "alert"}},
	{question: "Watering predictions?", answer: "Tracks moisture drying rate → predicts next watering. Accurate after 3-5 cycles.", category: "plants", tags: []string{"prediction", "watering", "forecast", "when", "water"}},
	{question: "Plant passport?", answer: "Shows: species, optimal conditions, current vs ideal readings, watering history, health score (0-100%).", category: "plants", tags: []string{"passport", "info", "details", "health", "score"}},
	{question: "Disease detection?", answer: "Monitors conditions correlated with disease (root rot=too wet, stress=too dry/cold). No direct diagnosis.", category: "plants", tags: []string{"disease", "health", "detect", "sick", "diagnosis"}},

	// NOTIFICATIONS (5)
	{question: "Notifications?", answer: "Push alerts: moisture below/above threshold, low battery, offline, watering prediction. Configure per device.", category: "notifications", tags: []string{"notification", "alert", "push", "warning"}},
	{question: "Custom thresholds?", answer: "Device Settings → Thresholds: moisture min/max, temp min/max, quiet hours, severity.", category: "notifications", tags: []string{"custom", "threshold", "alert", "set", "configure"}},
	{question: "Email notifications?", answer: "Push only for now. Email on roadmap. Can integrate Home Assistant for routing.", category: "notifications", tags: []string{"email", "notification", "send", "mail"}},
	{question: "Turn off notifications?", answer: "Per device: Settings→Notifications→OFF. All: App Settings→Disable All. Quiet hours available.", category: "notifications", tags: []string{"off", "disable", "silence", "mute", "notifications"}},
	{question: "Not getting notifications?", answer: "Check: app settings, phone permissions, battery saver, internet, threshold width. Android: exclude from battery optimization.", category: "notifications", tags: []string{"no", "not", "missing", "notifications", "broken"}},

	// HISTORY & DATA (5)
	{question: "Data retention?", answer: "Cloud: 12 months free. Sensor local: ~7 days. Export CSV anytime.", category: "history", tags: []string{"data", "storage", "history", "retention", "how long"}},
	{question: "Export data?", answer: "Device → History → Export (CSV/JSON). API: docs.zefir.app.", category: "history", tags: []string{"export", "csv", "download", "data", "backup"}},
	{question: "Charts?", answer: "Device → History: moisture/temp/battery. Ranges: 24h/7d/30d/90d/1y. Zoom + tap for values.", category: "history", tags: []string{"chart", "graph", "history", "view", "trend"}},
	{question: "Compare sensors?", answer: "Dashboard → Compare: overlay 2-4 sensors' charts.", category: "history", tags: []string{"compare", "multiple", "sensors", "overlay", "chart"}},
	{question: "API?", answer: "REST API at docs.zefir.app/api. Auth via API key. 100 req/min.", category: "history", tags: []string{"api", "developer", "rest", "integration", "endpoint"}},

	// SECURITY (5)
	{question: "Data private?", answer: "Only sensor data collected. TLS encrypted. No data sold. Email only for account.", category: "security", tags: []string{"privacy", "data", "private", "personal"}},
	{question: "Encryption?", answer: "TLS 1.3 transit, CCMP mesh, AES-256 at rest, SSP for BLE.", category: "security", tags: []string{"encrypt", "tls", "security", "protection"}},
	{question: "Data location?", answer: "Railway EU servers, PostgreSQL encrypted. Delete anytime in Account Settings.", category: "security", tags: []string{"storage", "server", "where", "cloud", "database"}},
	{question: "Others see sensors?", answer: "No, private. Optional read-only share links for specific devices.", category: "security", tags: []string{"share", "access", "private", "others", "see"}},
	{question: "App permissions?", answer: "BLE: pairing. Location(Android): OS req for BLE scan. Internet: sync. Camera: optional photos.", category: "security", tags: []string{"permission", "bluetooth", "location", "camera", "access"}},

	// TROUBLESHOOTING (7)
	{question: "Sensor offline?", answer: "Check battery (press button), WiFi/mesh status, distance. Dead battery: charge 30min. Password changed: factory reset.", category: "troubleshooting", tags: []string{"offline", "disconnected", "not working", "down"}},
	{question: "Wrong moisture?", answer: "Wait 5min to stabilize. Soil type affects readings. Recalibrate: Device Settings. Always 0/100: check probe.", category: "troubleshooting", tags: []string{"wrong", "incorrect", "reading", "moisture", "calibrate"}},
	{question: "Battery drain?", answer: "Normal 3-6mo at 30min. Fast drain: reduce interval, weak WiFi, cold, mesh relay.", category: "troubleshooting", tags: []string{"battery", "drain", "fast", "power", "life"}},
	{question: "App slow?", answer: "Restart app, clear cache, update, check internet.", category: "troubleshooting", tags: []string{"slow", "lag", "freeze", "app", "performance"}},
	{question: "Lost sensor?", answer: "App shows last location. MAC on sticker inside case. Remove from account if lost.", category: "troubleshooting", tags: []string{"lost", "find", "missing", "sensor", "location"}},
	{question: "Sensor in water?", answer: "NOT waterproof (IP44). Remove, dry 48h, don't charge wet. Probe is water-resistant.", category: "troubleshooting", tags: []string{"water", "wet", "dropped", "waterproof", "damage"}},
	{question: "Contact support?", answer: "Email: support@zefir.app (<24h). GitHub Issues. Telegram: t.me/zefirsensor. In-app: Settings → Report Bug.", category: "troubleshooting", tags: []string{"contact", "support", "help", "email", "report"}},
}

// createSearchFAQTool — search FAQ with 49 entries
func createSearchFAQTool() (tool.Tool, error) {
	type Input struct {
		Query string `json:"query"`
	}
	type Output struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "search_faq",
			Description: "Search FAQ (49 entries). Covers: general, sensors, setup, plants, notifications, data, security.",
		},
		func(ctx tool.Context, input Input) (Output, error) {
			log.Printf("[TOOL] search_faq called: query=%s", input.Query)

			if input.Query == "" {
				return Output{Result: "Error: search query is required"}, nil
			}

			query := strings.ToLower(input.Query)
			var matchKeys []int

			// Search by tags and question text
			for i, faq := range faqDatabase {
				for _, tag := range faq.tags {
					if strings.Contains(query, tag) || strings.Contains(tag, query) {
						if !slices.Contains(matchKeys, i) {
							matchKeys = append(matchKeys, i)
						}
						break
					}
				}
				if strings.Contains(strings.ToLower(faq.question), query) {
					if !slices.Contains(matchKeys, i) {
						matchKeys = append(matchKeys, i)
					}
				}
			}

			if len(matchKeys) == 0 {
				result := "No exact FAQ match found.\n\n"
				result += "Popular topics:\n"
				result += "  General: What is Zefir, pricing, platforms\n"
				result += "  Sensors: battery, accuracy, readings\n"
				result += "  Setup: pairing, WiFi, mesh\n"
				result += "  Plants: database, thresholds, predictions\n"
				result += "  Notifications: alerts, custom thresholds\n"
				result += "  Data: export, history, API\n"
				result += "  Security: privacy, encryption\n"
				result += "  Troubleshooting: offline, wrong readings\n"
				return Output{Result: result}, nil
			}

			if len(matchKeys) > 3 {
				matchKeys = matchKeys[:3]
			}

			result := fmt.Sprintf("FAQ RESULTS (%d matches):\n\n", len(matchKeys))
			for _, idx := range matchKeys {
				faq := faqDatabase[idx]
				result += fmt.Sprintf("Q: %s\n", faq.question)
				result += fmt.Sprintf("A: %s\n", faq.answer)
				result += fmt.Sprintf("[Category: %s]\n\n", faq.category)
			}

			return Output{Result: result}, nil
		},
	)
}

// createGetAppInfoTool — app info
func createGetAppInfoTool() (tool.Tool, error) {
	type Input struct {
		InfoType string `json:"infoType"` // all, platforms, languages, tech, license, requirements
	}
	type Output struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "get_app_info",
			Description: "App info: platforms/languages/tech/license/requirements.",
		},
		func(ctx tool.Context, input Input) (Output, error) {
			log.Printf("[TOOL] get_app_info called: infoType=%s", input.InfoType)

			infoType := strings.ToLower(input.InfoType)
			if infoType == "" {
				infoType = "all"
			}

			result := ""

			if infoType == "all" || infoType == "platforms" {
				result += "PLATFORMS: Windows/macOS/Linux (Tauri), iOS/Android, Web: zefir.app.\n"
			}

			if infoType == "all" || infoType == "languages" {
				result += "LANGUAGES: ru, en, de, es, pt, zh.\n"
			}

			if infoType == "all" || infoType == "tech" {
				result += "TECH: Tauri v2+Preact+TS, ESP-IDF(C), Go+PostgreSQL, ESP-NOW/BLE/WiFi, Railway EU.\n"
			}

			if infoType == "all" || infoType == "license" {
				result += "LICENSE: MIT, free for personal/commercial, source on GitHub.\n"
			}

			if infoType == "all" || infoType == "requirements" {
				result += "REQUIREMENTS: iOS 15+, Android 8+, Win 10+, macOS 11+, Ubuntu 20.04+, Chrome 80+/Firefox 78+/Safari 14+.\n"
			}

			return Output{Result: result}, nil
		},
	)
}

// createGetContactInfoTool — contact information
func createGetContactInfoTool() (tool.Tool, error) {
	type Input struct {
		ContactType string `json:"contactType"` // all, phone, email, social, address
	}
	type Output struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "get_contact_info",
			Description: "Contact info: email/phone/social/address.",
		},
		func(ctx tool.Context, input Input) (Output, error) {
			log.Printf("[TOOL] get_contact_info called: type=%s", input.ContactType)

			contactType := strings.ToLower(input.ContactType)
			if contactType == "" {
				contactType = "all"
			}

			result := ""

			if contactType == "all" || contactType == "phone" {
				result += "PHONE: +995 XXX XXX XXX (also WhatsApp), 9:00-22:00 daily.\n"
			}

			if contactType == "all" || contactType == "email" {
				result += "EMAIL: support@zefir.app (<24h), business@zefir.app.\n"
			}

			if contactType == "all" || contactType == "social" {
				result += "SOCIAL: Telegram t.me/zefirsensor, GitHub github.com/zefir-sensor, Instagram @zefirsensor.\n"
			}

			if contactType == "all" || contactType == "address" {
				result += "OFFICE: Tbilisi, Georgia. Mon-Fri 10:00-18:00.\n"
			}

			result += "Bugs: in-app 'Report Bug' or GitHub Issues."

			return Output{Result: result}, nil
		},
	)
}

// createGetFeatureGuideTool — feature guides
func createGetFeatureGuideTool() (tool.Tool, error) {
	type Input struct {
		Feature string `json:"feature"` // plant_passport, predictions, notifications, maps, home_assistant
	}
	type Output struct {
		Result string `json:"result"`
	}

	featureGuides := map[string]string{
		"plant_passport": `Plant's digital ID: species, optimal conditions, current vs ideal readings, health score (0-100%), watering history (30d chart), notes.
Access: tap device → "Plant Passport" tab, or Plants → tap name.
Score: 90-100=excellent, 70-89=good, 50-69=fair, <50=action needed.`,

		"predictions": `Tracks moisture drying rate → predicts next watering.
Accuracy: week 1 ±2 days, 3-5 cycles ±12h, 1 month ±6h.
Shows "Water in ~N days" on card + push notification.
Tips: keep sensor in place, don't change soil without recalibrating.`,

		"notifications": `Types: moisture low/high, battery <15%, offline >1h, watering prediction, temp out of range.
Per device: Device Settings → Notifications → toggle/thresholds/priority.
Global: App Settings → quiet hours, daily summary, sound/vibration.`,

		"maps": `Dashboard → Map icon → upload floor plan → drag sensors → save.
Color dots: green=ok, yellow=attention, red=alert. Tap for reading.
Shows mesh lines + signal strength. Multiple maps for rooms/floors.`,

		"home_assistant": `Setup: HACS → install Zefir → HA Settings → Integrations → Add Zefir → enter API key.
Entities: sensor.zefir_[name]_moisture/temperature/battery, binary_sensor online.
Automations: grow light, Telegram alerts, InfluxDB logging, smart irrigation.
MQTT: Account Settings → MQTT, topic: zefir/[device_id]/[type].`,
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "get_feature_guide",
			Description: "Feature guide: plant_passport/predictions/notifications/maps/home_assistant.",
		},
		func(ctx tool.Context, input Input) (Output, error) {
			log.Printf("[TOOL] get_feature_guide called: feature=%s", input.Feature)

			feature := strings.ToLower(input.Feature)
			if feature == "" {
				return Output{Result: "Error: feature name required. Available: plant_passport, predictions, notifications, maps, home_assistant."}, nil
			}

			guide, ok := featureGuides[feature]
			if !ok {
				return Output{Result: fmt.Sprintf("Unknown feature '%s'. Available: plant_passport, predictions, notifications, maps, home_assistant.", input.Feature)}, nil
			}

			return Output{Result: guide}, nil
		},
	)
}

// createGetSecurityInfoTool — security and privacy info
func createGetSecurityInfoTool() (tool.Tool, error) {
	type Input struct {
		Topic string `json:"topic"` // all, privacy, encryption, data_storage, permissions
	}
	type Output struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "get_security_info",
			Description: "Security info: privacy/encryption/data_storage/permissions.",
		},
		func(ctx tool.Context, input Input) (Output, error) {
			log.Printf("[TOOL] get_security_info called: topic=%s", input.Topic)

			topic := strings.ToLower(input.Topic)
			if topic == "" {
				topic = "all"
			}

			result := ""

			if topic == "all" || topic == "privacy" {
				result += "PRIVACY: Only sensor data collected. Email for account. No location/contacts/browsing. Never sell data. GDPR compliant, delete anytime.\n"
			}

			if topic == "all" || topic == "encryption" {
				result += "ENCRYPTION: TLS 1.3 transit, CCMP mesh, AES-256 at rest, SSP for BLE, HTTPS+API key.\n"
			}

			if topic == "all" || topic == "data_storage" {
				result += "STORAGE: Railway EU, PostgreSQL encrypted, 12mo free retention, daily backups. Sensor: ~7 days local. Export CSV/JSON.\n"
			}

			if topic == "all" || topic == "permissions" {
				result += "PERMISSIONS: BLE=pairing, Location(Android)=BLE scan req, Internet=sync, Notifications=alerts, Camera/Storage=optional.\n"
			}

			return Output{Result: result}, nil
		},
	)
}
