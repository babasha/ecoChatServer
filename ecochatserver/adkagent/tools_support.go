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
	// ===== GENERAL (7) =====
	{question: "What is Zefir?", answer: "Zefir is an IoT plant moisture monitoring system. It uses ESP32-C3 sensors to measure soil humidity, temperature, and environmental conditions. Data is sent to the Zefir cloud and displayed in a cross-platform app built with Tauri + Preact.", category: "general", tags: []string{"zefir", "what", "about", "overview"}},
	{question: "Is Zefir free?", answer: "Zefir is an open-source project (MIT license). The app and firmware are free. You need to purchase or build the ESP32-C3 sensor hardware. Cloud service is free for up to 10 devices per account.", category: "general", tags: []string{"free", "cost", "price", "open-source"}},
	{question: "What platforms does Zefir support?", answer: "Zefir app runs on Windows, macOS, Linux (via Tauri desktop), iOS, and Android. The web version works in any modern browser at zefir.app.", category: "general", tags: []string{"platform", "ios", "android", "windows", "mac", "linux"}},
	{question: "How many plants can I monitor?", answer: "Each Zefir sensor monitors one plant. You can have up to 10 sensors per mesh network and unlimited sensors per account (across multiple mesh networks).", category: "general", tags: []string{"plants", "limit", "how many", "sensors"}},
	{question: "What languages does Zefir support?", answer: "Zefir app is available in 6 languages: Russian (ru), English (en), German (de), Spanish (es), Portuguese (pt), and Chinese (zh).", category: "general", tags: []string{"language", "russian", "english", "german", "spanish"}},
	{question: "Do I need internet for Zefir?", answer: "Internet is needed for cloud sync and remote monitoring. However, sensors store readings locally and sync when connection restores. The mesh network works without internet — only the ROOT node needs WiFi for cloud access.", category: "general", tags: []string{"internet", "offline", "wifi", "cloud"}},
	{question: "Can I use Zefir outdoors?", answer: "Zefir sensors are designed for indoor use. They are not waterproof (IP rating: IP44 splash-proof only). For outdoor use, you'd need a weatherproof enclosure. The sensor probe itself is water-resistant.", category: "general", tags: []string{"outdoor", "waterproof", "garden", "outside"}},

	// ===== SENSORS (8) =====
	{question: "What sensor does Zefir use?", answer: "Zefir uses the ESP32-C3 microcontroller with a capacitive soil moisture sensor. The ESP32-C3 has built-in WiFi and Bluetooth LE, RISC-V processor, and ultra-low power modes.", category: "sensors", tags: []string{"sensor", "esp32", "hardware", "chip"}},
	{question: "How accurate is the moisture sensor?", answer: "The capacitive moisture sensor has ±3% accuracy in typical soil conditions. It measures relative moisture (0-100%). For best accuracy, calibrate in your specific soil type via Device Settings → Calibrate.", category: "sensors", tags: []string{"accuracy", "precision", "moisture", "calibrate"}},
	{question: "How long does the battery last?", answer: "Battery life depends on reading interval: 15 min = ~2 months, 30 min (default) = ~4 months, 1 hour = ~6 months. ROOT nodes use more power (~2 months). Cold temperatures reduce battery life.", category: "sensors", tags: []string{"battery", "life", "charge", "power"}},
	{question: "How do I charge the sensor?", answer: "Connect a USB-C cable. White LED = charging. Green LED = fully charged. Full charge takes about 2 hours. You can use any USB-C charger (5V).", category: "sensors", tags: []string{"charge", "usb", "power", "cable"}},
	{question: "Can I build my own sensor?", answer: "Yes! Zefir is open-source. Hardware designs and firmware are on GitHub. You need: ESP32-C3 dev board, capacitive soil sensor, 3.7V LiPo battery, and a 3D-printed case (STL files provided).", category: "sensors", tags: []string{"diy", "build", "custom", "open-source", "hardware"}},
	{question: "What is the sensor reading interval?", answer: "Default is every 30 minutes. Configurable from 15 minutes to 6 hours in Device Settings → Reading Interval. More frequent readings use more battery.", category: "sensors", tags: []string{"interval", "frequency", "reading", "schedule"}},
	{question: "Does the sensor measure air humidity?", answer: "No, the standard Zefir sensor measures SOIL moisture and SOIL temperature. Air humidity/temperature requires an additional DHT22 or BME280 module (optional add-on, see DIY guide).", category: "sensors", tags: []string{"air", "humidity", "temperature", "soil"}},
	{question: "Can the sensor measure light?", answer: "The standard sensor does not include a light sensor. However, an optional BH1750 light sensor module can be added (DIY add-on). The app supports light readings if the module is detected.", category: "sensors", tags: []string{"light", "lux", "sensor", "module"}},

	// ===== SETUP (6) =====
	{question: "How do I set up my first sensor?", answer: "1) Download Zefir app, 2) Open 'Add Device', 3) Hold sensor button 3 sec for BLE pairing, 4) Configure WiFi in the app, 5) Assign a plant, 6) Insert probe into soil. Full guide: use get_setup_guide tool.", category: "setup", tags: []string{"setup", "first", "start", "install", "begin"}},
	{question: "My sensor won't pair via Bluetooth", answer: "Ensure: 1) Sensor battery is charged (green LED on button press), 2) Bluetooth is ON on your phone, 3) Hold sensor button 3 sec for BLUE blink, 4) Phone is within 1 meter. Android users: grant Location permission. iOS users: check Zefir Bluetooth permission.", category: "setup", tags: []string{"bluetooth", "ble", "pair", "connect", "not found"}},
	{question: "WiFi setup failed", answer: "ESP32-C3 supports ONLY 2.4GHz WiFi (not 5GHz). Check: 1) Correct password, 2) 2.4GHz band enabled on router, 3) Sensor is within WiFi range, 4) Not using WEP (only WPA2/WPA3 supported). If your router merges bands, try disabling band steering.", category: "setup", tags: []string{"wifi", "network", "connect", "fail", "password"}},
	{question: "How do I add a second sensor?", answer: "Same process as first sensor. The second sensor automatically joins the mesh network as a NODE (first sensor is ROOT). Place within 10m of any existing sensor. The app guides you through mesh auto-discovery.", category: "setup", tags: []string{"second", "add", "another", "new", "mesh"}},
	{question: "How do I factory reset?", answer: "Hold the sensor button for 10 seconds until all LEDs flash. This erases WiFi credentials, mesh config, and plant assignments. The sensor returns to factory defaults and needs fresh setup.", category: "setup", tags: []string{"reset", "factory", "default", "erase", "clear"}},
	{question: "Can I move a sensor to a different plant?", answer: "Yes! In the app: tap device → Edit → Change Plant. Select a new plant from the database. Sensor thresholds auto-update to match the new plant's needs.", category: "setup", tags: []string{"move", "change", "plant", "reassign", "different"}},

	// ===== PLANTS (6) =====
	{question: "How many plants are in the database?", answer: "Zefir has 89 plant species in 5 categories: Tropical (25), Succulents (20), Herbs (15), Vegetables (14), Flowering (15). Each has pre-configured moisture and temperature thresholds for your Zefir sensor.", category: "plants", tags: []string{"database", "species", "how many", "plants", "list"}},
	{question: "Can I add a custom plant?", answer: "Yes! When assigning a plant, tap 'Custom Plant'. Enter name, set moisture thresholds (min/max %), temperature range, and optionally add a photo. Custom plants work exactly like database plants for alerts.", category: "plants", tags: []string{"custom", "add", "new", "plant", "own"}},
	{question: "What are moisture thresholds?", answer: "Each plant has min and max soil moisture percentages. Below min → 'Too dry' alert (water needed). Above max → 'Too wet' alert (overwatering risk). Zefir pre-configures these from the plant database, but you can customize them.", category: "plants", tags: []string{"threshold", "moisture", "min", "max", "alert"}},
	{question: "How do watering predictions work?", answer: "Zefir tracks moisture trends over time. Using the drying rate (how fast moisture drops), it predicts when the plant will need water next. Predictions improve with more data — usually accurate after 3-5 watering cycles.", category: "plants", tags: []string{"prediction", "watering", "forecast", "when", "water"}},
	{question: "What does the plant passport show?", answer: "The Plant Passport displays: species info, optimal conditions (moisture, temp, light), your sensor's current readings vs. ideal range, watering history, growth notes, and a health score (0-100%).", category: "plants", tags: []string{"passport", "info", "details", "health", "score"}},
	{question: "Can Zefir detect plant diseases?", answer: "Zefir monitors environmental conditions (moisture, temperature) that correlate with disease risk. It can warn about conditions favorable for root rot (too wet) or stress (too dry, too cold). It does not directly diagnose diseases.", category: "plants", tags: []string{"disease", "health", "detect", "sick", "diagnosis"}},

	// ===== NOTIFICATIONS (5) =====
	{question: "How do notifications work?", answer: "Zefir sends push notifications when: soil moisture drops below min threshold, moisture exceeds max, battery is low (<15%), sensor goes offline, or based on watering predictions. Configure per-device in Device Settings → Notifications.", category: "notifications", tags: []string{"notification", "alert", "push", "warning"}},
	{question: "Can I set custom alert thresholds?", answer: "Yes! Go to Device Settings → Thresholds. Set custom moisture min/max and temperature min/max. You can also set quiet hours (no notifications) and choose alert severity levels.", category: "notifications", tags: []string{"custom", "threshold", "alert", "set", "configure"}},
	{question: "Can I get email notifications?", answer: "Currently Zefir supports push notifications (mobile/desktop) only. Email notifications are on the roadmap. You can also integrate with Home Assistant for advanced notification routing.", category: "notifications", tags: []string{"email", "notification", "send", "mail"}},
	{question: "How do I turn off notifications?", answer: "Per device: Device Settings → Notifications → toggle OFF. All notifications: App Settings → Notifications → Disable All. Quiet hours: App Settings → Notifications → Quiet Hours.", category: "notifications", tags: []string{"off", "disable", "silence", "mute", "notifications"}},
	{question: "I'm not getting notifications", answer: "Check: 1) Notifications enabled in Zefir app settings, 2) Phone notification permissions for Zefir, 3) Battery saver not blocking Zefir, 4) Internet connection, 5) Device thresholds properly set (not too wide). On Android, exclude Zefir from battery optimization.", category: "notifications", tags: []string{"no", "not", "missing", "notifications", "broken"}},

	// ===== HISTORY & DATA (5) =====
	{question: "How long is data stored?", answer: "Cloud storage: 12 months of readings for free accounts. Sensor local storage: ~7 days of readings (syncs to cloud when connected). You can export data as CSV anytime from the app.", category: "history", tags: []string{"data", "storage", "history", "retention", "how long"}},
	{question: "Can I export my data?", answer: "Yes! In the app: Device → History → Export (CSV or JSON). Exports include all readings with timestamps. API access also available for developers (see API docs at docs.zefir.app).", category: "history", tags: []string{"export", "csv", "download", "data", "backup"}},
	{question: "How do I view historical charts?", answer: "In the app: tap a device → History tab. View moisture, temperature, and battery charts. Time ranges: 24h, 7d, 30d, 90d, 1y. Pinch to zoom on mobile. Tap data points for exact values.", category: "history", tags: []string{"chart", "graph", "history", "view", "trend"}},
	{question: "Can I compare multiple sensors?", answer: "Yes! In the app: Dashboard → tap 'Compare' icon. Select 2-4 sensors to overlay their moisture/temperature charts. Useful for plants in different locations or comparing soil types.", category: "history", tags: []string{"compare", "multiple", "sensors", "overlay", "chart"}},
	{question: "Is there an API?", answer: "Yes! Zefir has a REST API for developers. Docs at docs.zefir.app/api. Endpoints for devices, readings, user data. Authenticated via API key (generate in Account Settings). Rate limit: 100 req/min.", category: "history", tags: []string{"api", "developer", "rest", "integration", "endpoint"}},

	// ===== SECURITY (5) =====
	{question: "Is my data private?", answer: "Yes. Zefir collects only sensor data (moisture, temperature, battery) and device metadata. No personal data beyond your email for account. Data is encrypted in transit (TLS) and at rest. We never sell data to third parties.", category: "security", tags: []string{"privacy", "data", "private", "personal"}},
	{question: "How is data encrypted?", answer: "All data: TLS 1.3 in transit (sensor→cloud, app→cloud). Mesh: ESP-NOW uses CCMP encryption (same as WPA2). Cloud: AES-256 encryption at rest. BLE pairing: Secure Simple Pairing (SSP).", category: "security", tags: []string{"encrypt", "tls", "security", "protection"}},
	{question: "Where is data stored?", answer: "Zefir cloud runs on Railway (EU servers). Sensor data is stored in PostgreSQL with encryption at rest. Backups are encrypted and stored separately. You can delete all your data at any time from Account Settings.", category: "security", tags: []string{"storage", "server", "where", "cloud", "database"}},
	{question: "Can others see my sensors?", answer: "No. Each account has private devices. Sharing features: you can generate a read-only share link for specific devices (e.g., for a plant sitter). Shared users cannot modify settings or access your account.", category: "security", tags: []string{"share", "access", "private", "others", "see"}},
	{question: "What permissions does the app need?", answer: "Bluetooth: for sensor pairing and local communication. Location (Android only): required by OS for BLE scanning (not used for tracking). Internet: for cloud sync. Notifications: for plant alerts. Camera (optional): for plant photos.", category: "security", tags: []string{"permission", "bluetooth", "location", "camera", "access"}},

	// ===== TROUBLESHOOTING (7) =====
	{question: "Sensor shows offline", answer: "Check: 1) Battery (press button for LED), 2) WiFi status (ROOT) or mesh connection (NODE), 3) Distance to router/other sensors. If battery dead, charge 30 min then retry. If WiFi password changed, factory reset and re-setup.", category: "troubleshooting", tags: []string{"offline", "disconnected", "not working", "down"}},
	{question: "Moisture reading seems wrong", answer: "Capacitive sensors need 5 min to stabilize after insertion. Different soil types give different baseline readings. Recalibrate: Device Settings → Calibrate. If reading is always 0 or 100, check probe connection.", category: "troubleshooting", tags: []string{"wrong", "incorrect", "reading", "moisture", "calibrate"}},
	{question: "Battery drains too fast", answer: "Normal: 3-6 months at 30-min interval. Causes of fast drain: reading interval too frequent, weak WiFi signal (causes retries), cold temperatures, mesh relay duty. Increase interval or use mesh to reduce WiFi usage.", category: "troubleshooting", tags: []string{"battery", "drain", "fast", "power", "life"}},
	{question: "App is slow or laggy", answer: "Try: 1) Close and reopen app, 2) Clear app cache, 3) Check internet connection, 4) Update app to latest version. Desktop app: close other Tauri/Electron apps if memory is low. If persistent, report bug via app.", category: "troubleshooting", tags: []string{"slow", "lag", "freeze", "app", "performance"}},
	{question: "I lost my sensor", answer: "Check the app: Dashboard shows last known location (if GPS was enabled on phone during setup). The sensor's MAC address is also on the sticker inside the case. If truly lost, you can remove it from your account and set up a new one.", category: "troubleshooting", tags: []string{"lost", "find", "missing", "sensor", "location"}},
	{question: "Sensor fell into water", answer: "The sensor is NOT waterproof (IP44 only). If it fell in water: 1) Remove immediately, 2) Disconnect battery if possible, 3) Let it dry for 48 hours (rice helps), 4) Do NOT try to charge while wet, 5) Test after fully dry. The probe itself is water-resistant.", category: "troubleshooting", tags: []string{"water", "wet", "dropped", "waterproof", "damage"}},
	{question: "How do I contact support?", answer: "Email: support@zefir.app (response < 24h). GitHub Issues: for bugs and feature requests. Telegram: t.me/zefirsensor. In-app: Settings → Report Bug. For hardware issues, include your sensor MAC address.", category: "troubleshooting", tags: []string{"contact", "support", "help", "email", "report"}},
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
			Description: "Search Zefir FAQ knowledge base (49 entries in 8 categories). Use when user asks common questions about Zefir, sensors, setup, plants, notifications, data, security, or troubleshooting.",
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
			Description: "Get Zefir app information: platforms, languages, tech stack, license, system requirements. Use when user asks 'what platforms', 'system requirements', 'is it open source', 'tech stack'.",
		},
		func(ctx tool.Context, input Input) (Output, error) {
			log.Printf("[TOOL] get_app_info called: infoType=%s", input.InfoType)

			infoType := strings.ToLower(input.InfoType)
			if infoType == "" {
				infoType = "all"
			}

			result := "ZEFIR APP INFO\n\n"

			if infoType == "all" || infoType == "platforms" {
				result += "PLATFORMS:\n"
				result += "  Desktop: Windows, macOS, Linux (Tauri)\n"
				result += "  Mobile: iOS, Android (Tauri Mobile)\n"
				result += "  Web: zefir.app (any modern browser)\n\n"
			}

			if infoType == "all" || infoType == "languages" {
				result += "LANGUAGES:\n"
				result += "  Russian (ru), English (en), German (de)\n"
				result += "  Spanish (es), Portuguese (pt), Chinese (zh)\n\n"
			}

			if infoType == "all" || infoType == "tech" {
				result += "TECH STACK:\n"
				result += "  App: Tauri v2 + Preact + TypeScript\n"
				result += "  Firmware: ESP-IDF (C) for ESP32-C3\n"
				result += "  Backend: Go + PostgreSQL\n"
				result += "  Protocols: ESP-NOW mesh, BLE, WiFi 2.4GHz\n"
				result += "  Cloud: Railway (EU)\n\n"
			}

			if infoType == "all" || infoType == "license" {
				result += "LICENSE:\n"
				result += "  MIT License (open-source)\n"
				result += "  Free for personal and commercial use\n"
				result += "  Source code on GitHub\n\n"
			}

			if infoType == "all" || infoType == "requirements" {
				result += "REQUIREMENTS:\n"
				result += "  iOS: 15.0+\n"
				result += "  Android: 8.0+ (API 26)\n"
				result += "  Windows: 10+\n"
				result += "  macOS: 11+\n"
				result += "  Linux: Ubuntu 20.04+ (or equivalent)\n"
				result += "  Browser: Chrome 80+, Firefox 78+, Safari 14+\n"
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
			Description: "Get Zefir contact information: email, phone, social media, office address. Use when user asks 'how to contact', 'support email', 'phone number', 'social media'.",
		},
		func(ctx tool.Context, input Input) (Output, error) {
			log.Printf("[TOOL] get_contact_info called: type=%s", input.ContactType)

			contactType := strings.ToLower(input.ContactType)
			if contactType == "" {
				contactType = "all"
			}

			result := "CONTACT INFORMATION\n\n"

			if contactType == "all" || contactType == "phone" {
				result += "PHONE:\n"
				result += "  Main: +995 XXX XXX XXX\n"
				result += "  WhatsApp: +995 XXX XXX XXX\n"
				result += "  Available: 9:00-22:00 daily\n\n"
			}

			if contactType == "all" || contactType == "email" {
				result += "EMAIL:\n"
				result += "  Support: support@zefir.app\n"
				result += "  Business: business@zefir.app\n"
				result += "  Response time: < 24 hours\n\n"
			}

			if contactType == "all" || contactType == "social" {
				result += "SOCIAL & COMMUNITY:\n"
				result += "  Telegram: t.me/zefirsensor\n"
				result += "  GitHub: github.com/zefir-sensor\n"
				result += "  Instagram: @zefirsensor\n"
				result += "  Facebook: facebook.com/zefirsensor\n\n"
			}

			if contactType == "all" || contactType == "address" {
				result += "OFFICE:\n"
				result += "  Tbilisi, Georgia\n"
				result += "  Business hours: Mon-Fri 10:00-18:00\n"
			}

			result += "\nFor bugs: use in-app 'Report Bug' or GitHub Issues."

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
		"plant_passport": `PLANT PASSPORT

The Plant Passport is your plant's digital identity card:

What it shows:
- Species info (common name, scientific name, origin)
- Optimal conditions (moisture, temperature, light)
- Current readings vs. ideal ranges
- Health score (0-100% based on how close readings are to optimal)
- Watering history (last 30 days chart)
- Growth notes (add your own observations)
- Sensor assignment and device info

How to access:
- Tap any device on dashboard → "Plant Passport" tab
- Or: Plants section → tap plant name

Health Score calculation:
- 90-100%: Excellent — conditions are optimal
- 70-89%: Good — minor deviations
- 50-69%: Fair — some conditions need attention
- <50%: Poor — immediate action needed`,

		"predictions": `WATERING PREDICTIONS

Zefir learns your plant's water consumption pattern:

How it works:
1. Tracks moisture level over time
2. Calculates drying rate (% per hour)
3. Considers: soil type, plant species, season, temperature
4. Predicts when moisture will drop below threshold

Accuracy:
- First week: rough estimates (~±2 days)
- After 3-5 cycles: good accuracy (~±12 hours)
- After 1 month: very accurate (~±6 hours)

What you see:
- "Water in ~2 days" on device card
- Push notification when predicted time is near
- Timeline visualization in Plant Passport

Tips for better predictions:
- Keep sensor in same spot
- Don't change soil or pot without recalibrating
- Consistent watering helps the algorithm learn faster`,

		"notifications": `NOTIFICATIONS GUIDE

Types of notifications:
1. Moisture Low: soil below min threshold → water needed
2. Moisture High: soil above max threshold → overwatering risk
3. Low Battery: sensor battery < 15%
4. Offline: sensor lost connection for > 1 hour
5. Watering Prediction: reminder based on prediction
6. Temperature: outside plant's comfort range

Configuration (per device):
- Device Settings → Notifications
- Toggle each type ON/OFF
- Set custom thresholds (override plant defaults)
- Set priority: normal / urgent / silent

Global settings:
- App Settings → Notifications
- Quiet hours (no notifications during sleep)
- Daily summary (one notification per day with all plants)
- Sound / vibration preferences`,

		"maps": `SENSOR MAP / FLOOR PLAN

The Map feature helps visualize sensor locations:

Setup:
1. Dashboard → Map view (top-right icon)
2. Upload floor plan or room photo
3. Drag sensor icons onto the map
4. Save layout

Features:
- Color-coded sensor dots (green=ok, yellow=attention, red=alert)
- Tap dot for quick reading popup
- Mesh network lines shown between sensors
- Signal strength indicator on connections
- Multiple maps for different rooms/floors

Tips:
- Take a top-down photo of each room
- Use the grid overlay for precise placement
- Great for visualizing mesh network coverage`,

		"home_assistant": `HOME ASSISTANT INTEGRATION

Zefir integrates with Home Assistant for smart home automation:

Setup:
1. Install Zefir integration from HACS
2. In HA: Settings → Integrations → Add → Zefir
3. Enter your Zefir API key (from Account Settings)
4. Sensors appear automatically as HA entities

Available entities:
- sensor.zefir_[name]_moisture (%)
- sensor.zefir_[name]_temperature (°C)
- sensor.zefir_[name]_battery (%)
- binary_sensor.zefir_[name]_online (on/off)

Automation examples:
- Turn on grow light when moisture is low
- Send Telegram message when plant needs water
- Log all readings to InfluxDB/Grafana
- Control smart irrigation valve based on moisture

MQTT alternative:
- Zefir also supports MQTT output
- Configure in Account Settings → MQTT
- Topic format: zefir/[device_id]/[reading_type]`,
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "get_feature_guide",
			Description: "Get guide for Zefir features: 'plant_passport', 'predictions', 'notifications', 'maps', 'home_assistant'. Use when user asks about specific app features, how predictions work, Home Assistant integration, etc.",
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
			Description: "Get Zefir security and privacy information: data privacy, encryption, storage, app permissions. Use when user asks about privacy, security, data protection, permissions.",
		},
		func(ctx tool.Context, input Input) (Output, error) {
			log.Printf("[TOOL] get_security_info called: topic=%s", input.Topic)

			topic := strings.ToLower(input.Topic)
			if topic == "" {
				topic = "all"
			}

			result := "ZEFIR SECURITY & PRIVACY\n\n"

			if topic == "all" || topic == "privacy" {
				result += "PRIVACY:\n"
				result += "  We collect: sensor data (moisture, temp, battery)\n"
				result += "  We store: email (for account), device metadata\n"
				result += "  We DON'T collect: location, contacts, browsing history\n"
				result += "  We NEVER sell data to third parties\n"
				result += "  GDPR compliant — request data deletion anytime\n\n"
			}

			if topic == "all" || topic == "encryption" {
				result += "ENCRYPTION:\n"
				result += "  In transit: TLS 1.3 (sensor to cloud, app to cloud)\n"
				result += "  Mesh: ESP-NOW CCMP encryption (WPA2-level)\n"
				result += "  At rest: AES-256 (cloud database)\n"
				result += "  BLE: Secure Simple Pairing (SSP)\n"
				result += "  API: HTTPS only, API key authentication\n\n"
			}

			if topic == "all" || topic == "data_storage" {
				result += "DATA STORAGE:\n"
				result += "  Cloud: Railway platform (EU servers)\n"
				result += "  Database: PostgreSQL with encryption\n"
				result += "  Retention: 12 months (free), longer for paid\n"
				result += "  Backups: daily, encrypted, geo-redundant\n"
				result += "  Local: sensor stores ~7 days of readings\n"
				result += "  Export: CSV/JSON anytime from app\n\n"
			}

			if topic == "all" || topic == "permissions" {
				result += "APP PERMISSIONS:\n"
				result += "  Bluetooth: sensor pairing and communication\n"
				result += "  Location (Android): required by OS for BLE scanning\n"
				result += "  Internet: cloud sync and remote monitoring\n"
				result += "  Notifications: plant alerts and reminders\n"
				result += "  Camera (optional): plant photos only\n"
				result += "  Storage (optional): data export\n"
			}

			return Output{Result: result}, nil
		},
	)
}
