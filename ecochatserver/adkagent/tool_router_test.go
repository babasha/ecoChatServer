package adkagent

import (
	"context"
	"fmt"
	"testing"
	"time"

	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/genai"
)

// mockTool creates a simple tool with the given name and description.
func mockTool(name, description string) tool.Tool {
	type Input struct{}
	type Output struct{ Result string }

	t, err := functiontool.New(
		functiontool.Config{
			Name:        name,
			Description: description,
		},
		func(ctx tool.Context, input Input) (Output, error) {
			return Output{Result: "ok"}, nil
		},
	)
	if err != nil {
		panic(err)
	}
	return t
}

// mockReadonlyContext implements agent.ReadonlyContext for testing.
type mockReadonlyContext struct {
	context.Context
	content *genai.Content
}

func (m *mockReadonlyContext) UserContent() *genai.Content            { return m.content }
func (m *mockReadonlyContext) InvocationID() string                   { return "test" }
func (m *mockReadonlyContext) AgentName() string                      { return "test_agent" }
func (m *mockReadonlyContext) UserID() string                         { return "user1" }
func (m *mockReadonlyContext) AppName() string                        { return "test_app" }
func (m *mockReadonlyContext) SessionID() string                      { return "session1" }
func (m *mockReadonlyContext) Branch() string                         { return "" }
func (m *mockReadonlyContext) ReadonlyState() session.ReadonlyState   { return nil }
func (m *mockReadonlyContext) Deadline() (time.Time, bool)            { return time.Time{}, false }
func (m *mockReadonlyContext) Done() <-chan struct{}                   { return nil }
func (m *mockReadonlyContext) Err() error                             { return nil }
func (m *mockReadonlyContext) Value(key interface{}) interface{}      { return nil }

func newMockContext(text string) *mockReadonlyContext {
	return &mockReadonlyContext{
		content: genai.NewContentFromText(text, genai.RoleUser),
	}
}

// createTestRouter creates a ToolRouter with mock tools for testing.
func createTestRouter() *ToolRouter {
	plantTools := []tool.Tool{
		mockTool("search_plant", "Search plant by name/tag"),
		mockTool("get_plant_categories", "List plant categories"),
		mockTool("get_plants_by_category", "Get plants in category: tropical/succulent/herb/vegetable/flowering"),
		mockTool("get_plant_care", "Get care guide: humidity, temperature, light, watering"),
		mockTool("compare_plants", "Compare 2-4 plants"),
		mockTool("recommend_plants", "Recommend plants by criteria"),
	}

	deviceTools := []tool.Tool{
		mockTool("get_user_devices", "List user sensor devices"),
		mockTool("get_sensor_reading", "Get latest sensor reading"),
		mockTool("get_setup_guide", "Setup guide: overview/unboxing/ble_pairing/wifi_config"),
		mockTool("troubleshoot_device", "Troubleshoot sensor issues"),
		mockTool("get_mesh_info", "Mesh network info: overview/root_node/esp_now/topology"),
	}

	supportTools := []tool.Tool{
		mockTool("search_faq", "Search FAQ (49 entries)"),
		mockTool("get_app_info", "App info: platforms/languages/tech"),
		mockTool("get_contact_info", "Contact info: email/phone/social"),
		mockTool("get_feature_guide", "Feature guide: plant_passport/predictions"),
		mockTool("get_security_info", "Security info: privacy/encryption"),
	}

	return NewToolRouter(plantTools, deviceTools, supportTools)
}

func toolNames(tools []tool.Tool) []string {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name()
	}
	return names
}

func hasToolNamed(tools []tool.Tool, name string) bool {
	for _, t := range tools {
		if t.Name() == name {
			return true
		}
	}
	return false
}

func TestToolRouterName(t *testing.T) {
	tr := createTestRouter()
	if tr.Name() != "zefir_tool_router" {
		t.Errorf("expected name 'zefir_tool_router', got %q", tr.Name())
	}
}

func TestToolRouterPlantMessageEN(t *testing.T) {
	tr := createTestRouter()
	ctx := newMockContext("How to care for monstera?")

	tools, err := tr.Tools(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should include plant tools
	if !hasToolNamed(tools, "search_plant") {
		t.Error("expected search_plant tool for plant query")
	}
	if !hasToolNamed(tools, "get_plant_care") {
		t.Error("expected get_plant_care tool for plant query")
	}
	// Should include search_faq (always included)
	if !hasToolNamed(tools, "search_faq") {
		t.Error("expected search_faq tool (always included)")
	}
	// Should NOT include all 16 tools
	if len(tools) >= 16 {
		t.Errorf("expected fewer than 16 tools, got %d: %v", len(tools), toolNames(tools))
	}
	t.Logf("Plant query selected %d tools: %v", len(tools), toolNames(tools))
}

func TestToolRouterDeviceMessageRU(t *testing.T) {
	tr := createTestRouter()
	ctx := newMockContext("Мой датчик не подключается")

	tools, err := tr.Tools(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should include device tools
	if !hasToolNamed(tools, "troubleshoot_device") {
		t.Error("expected troubleshoot_device tool for device query in Russian")
	}
	if !hasToolNamed(tools, "get_user_devices") {
		t.Error("expected get_user_devices tool for device query in Russian")
	}
	// Should NOT include all 16 tools
	if len(tools) >= 16 {
		t.Errorf("expected fewer than 16 tools, got %d: %v", len(tools), toolNames(tools))
	}
	t.Logf("Device query (RU) selected %d tools: %v", len(tools), toolNames(tools))
}

func TestToolRouterSupportMessage(t *testing.T) {
	tr := createTestRouter()
	ctx := newMockContext("Is Zefir free?")

	tools, err := tr.Tools(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should include support tools
	if !hasToolNamed(tools, "search_faq") {
		t.Error("expected search_faq tool for support query")
	}
	// Should NOT include all 16 tools
	if len(tools) >= 16 {
		t.Errorf("expected fewer than 16 tools, got %d: %v", len(tools), toolNames(tools))
	}
	t.Logf("Support query selected %d tools: %v", len(tools), toolNames(tools))
}

func TestToolRouterFallbackAmbiguous(t *testing.T) {
	tr := createTestRouter()
	ctx := newMockContext("привет")

	tools, err := tr.Tools(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// "привет" is a stop word → no keywords match → fallback to all tools
	if len(tools) != 16 {
		t.Errorf("expected all 16 tools for ambiguous query, got %d: %v", len(tools), toolNames(tools))
	}
	t.Logf("Ambiguous query selected %d tools (fallback)", len(tools))
}

func TestToolRouterEmptyMessage(t *testing.T) {
	tr := createTestRouter()
	ctx := newMockContext("")

	tools, err := tr.Tools(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Empty message → fallback to all tools
	if len(tools) != 16 {
		t.Errorf("expected all 16 tools for empty message, got %d", len(tools))
	}
}

func TestToolRouterMixedQuery(t *testing.T) {
	tr := createTestRouter()
	ctx := newMockContext("my plant sensor battery")

	tools, err := tr.Tools(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should include both plant and device groups
	if !hasToolNamed(tools, "search_plant") {
		t.Error("expected search_plant for mixed plant+device query")
	}
	if !hasToolNamed(tools, "get_user_devices") {
		t.Error("expected get_user_devices for mixed plant+device query")
	}
	if !hasToolNamed(tools, "search_faq") {
		t.Error("expected search_faq (always included)")
	}
	t.Logf("Mixed query selected %d tools: %v", len(tools), toolNames(tools))
}

func TestToolRouterPlantQueryRU(t *testing.T) {
	tr := createTestRouter()
	ctx := newMockContext("Как ухаживать за монстерой?")

	tools, err := tr.Tools(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should include plant tools via synonym expansion
	if !hasToolNamed(tools, "search_plant") {
		t.Error("expected search_plant for Russian plant query")
	}
	if len(tools) >= 16 {
		t.Errorf("expected fewer than 16 tools, got %d: %v", len(tools), toolNames(tools))
	}
	t.Logf("Plant query (RU) selected %d tools: %v", len(tools), toolNames(tools))
}

func TestToolRouterDeviceQueryEN(t *testing.T) {
	tr := createTestRouter()
	ctx := newMockContext("My sensor won't connect")

	tools, err := tr.Tools(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !hasToolNamed(tools, "troubleshoot_device") {
		t.Error("expected troubleshoot_device for English device query")
	}
	if len(tools) >= 16 {
		t.Errorf("expected fewer than 16 tools, got %d: %v", len(tools), toolNames(tools))
	}
	t.Logf("Device query (EN) selected %d tools: %v", len(tools), toolNames(tools))
}

func TestToolRouterSearchFAQAlwaysIncluded(t *testing.T) {
	tr := createTestRouter()

	// A plant-only query should still get search_faq
	ctx := newMockContext("monstera watering guide")

	tools, err := tr.Tools(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !hasToolNamed(tools, "search_faq") {
		t.Error("search_faq should always be included even for plant-only queries")
	}
}

func TestTokenize(t *testing.T) {
	tokens := tokenize("Мой датчик не подключается")
	// "мой" and "не" are stop words → filtered out
	// "датчик" → synonym "sensor"
	// "подключается" → no synonym (but "подключить" would match)
	found := map[string]bool{}
	for _, tok := range tokens {
		found[tok] = true
	}

	if !found["sensor"] {
		t.Error("expected 'sensor' from synonym expansion of 'датчик'")
	}
	if found["мой"] {
		t.Error("'мой' should be filtered as stop word")
	}
	if found["не"] {
		t.Error("'не' should be filtered as stop word")
	}
	t.Logf("Tokens: %v", tokens)
}

func TestTokenizeEnglish(t *testing.T) {
	tokens := tokenize("How to care for my monstera?")
	found := map[string]bool{}
	for _, tok := range tokens {
		found[tok] = true
	}

	if !found["care"] {
		t.Error("expected 'care' token")
	}
	if !found["monstera"] {
		t.Error("expected 'monstera' token")
	}
	if found["how"] {
		t.Error("'how' should be filtered as stop word")
	}
	if found["my"] {
		t.Error("'my' should be filtered as stop word")
	}
	t.Logf("Tokens: %v", tokens)
}

func TestGetFAQTags(t *testing.T) {
	tags := GetFAQTags()
	if len(tags) == 0 {
		t.Error("expected non-empty FAQ tags")
	}
	// Check some known tags
	tagSet := map[string]bool{}
	for _, tag := range tags {
		tagSet[tag] = true
	}
	for _, expected := range []string{"zefir", "sensor", "battery", "setup", "wifi"} {
		if !tagSet[expected] {
			t.Errorf("expected FAQ tag %q", expected)
		}
	}
	t.Logf("FAQ tags: %d unique tags", len(tags))
}

// ============================================================================
// QUALITY ANALYSIS — 10 real-world queries
// ============================================================================

// qualityCase describes a test scenario with expected routing results.
type qualityCase struct {
	name           string   // short label
	query          string   // user message
	expectedGroups []string // which groups MUST be selected
	mustHaveTools  []string // tools that MUST be in the result
	mustNotTools   []string // tools that MUST NOT be in the result (unless fallback)
	maxTools       int      // ideal max tools (0 = any)
}

func TestToolRouterQualityAnalysis(t *testing.T) {
	tr := createTestRouter()

	cases := []qualityCase{
		{
			name:           "1. Plant care EN",
			query:          "How do I take care of my orchid?",
			expectedGroups: []string{"plant"},
			mustHaveTools:  []string{"search_plant", "get_plant_care", "search_faq"},
			mustNotTools:   []string{"troubleshoot_device", "get_mesh_info"},
			maxTools:       8,
		},
		{
			name:           "2. Plant care RU",
			query:          "Как часто поливать кактус?",
			expectedGroups: []string{"plant"},
			mustHaveTools:  []string{"search_plant", "get_plant_care", "search_faq"},
			mustNotTools:   []string{"get_user_devices", "get_setup_guide"},
			maxTools:       8,
		},
		{
			name:           "3. Device setup EN",
			query:          "How to connect my new sensor to WiFi?",
			expectedGroups: []string{"device"},
			mustHaveTools:  []string{"get_setup_guide", "troubleshoot_device", "search_faq"},
			mustNotTools:   []string{"search_plant", "compare_plants"},
			maxTools:       8,
		},
		{
			name:           "4. Troubleshoot RU",
			query:          "Сенсор показывает неправильную влажность, что делать?",
			expectedGroups: []string{"device"},
			mustHaveTools:  []string{"troubleshoot_device", "search_faq"},
			mustNotTools:   []string{"compare_plants", "recommend_plants"},
			maxTools:       12,
		},
		{
			name:           "5. Battery DE",
			query:          "Wie lange hält die Batterie vom Sensor?",
			expectedGroups: []string{"device"},
			mustHaveTools:  []string{"search_faq"},
			mustNotTools:   []string{"search_plant", "compare_plants"},
			maxTools:       12,
		},
		{
			name:           "6. App info ES",
			query:          "¿En qué plataformas funciona la aplicación?",
			expectedGroups: []string{"support"},
			mustHaveTools:  []string{"get_app_info", "search_faq"},
			mustNotTools:   []string{"search_plant", "get_sensor_reading"},
			maxTools:       8,
		},
		{
			name:           "7. Security question",
			query:          "Is my data encrypted? Where are the servers?",
			expectedGroups: []string{"support"},
			mustHaveTools:  []string{"get_security_info", "search_faq"},
			mustNotTools:   []string{"search_plant", "get_mesh_info"},
			maxTools:       8,
		},
		{
			name:           "8. Mesh network",
			query:          "Explain how mesh network topology works between sensors",
			expectedGroups: []string{"device"},
			mustHaveTools:  []string{"get_mesh_info", "search_faq"},
			mustNotTools:   []string{"search_plant", "recommend_plants"},
			maxTools:       8,
		},
		{
			name:           "9. Greeting fallback",
			query:          "Добрый день!",
			expectedGroups: []string{}, // empty = expect fallback to all 16
			mustHaveTools:  []string{"search_faq", "search_plant", "troubleshoot_device"},
			mustNotTools:   []string{},
			maxTools:       16,
		},
		{
			name:           "10. ZH plant query",
			query:          "我的植物湿度太低了怎么办?",
			expectedGroups: []string{"plant"},
			mustHaveTools:  []string{"search_plant", "search_faq"},
			mustNotTools:   []string{"get_mesh_info"},
			maxTools:       12,
		},
	}

	t.Logf("%s", "")
	t.Logf("%s", "====================================================================================================")
	t.Logf("%s", "  TOOL ROUTER QUALITY ANALYSIS — 10 real-world queries")
	t.Logf("%s", "====================================================================================================")

	passed := 0
	warned := 0
	failed := 0

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := newMockContext(tc.query)

			// Get tokens for debug
			tokens := tokenize(tc.query)

			// Score each group manually
			scores := map[string]float64{}
			for _, g := range tr.groups {
				scores[g.name] = scoreGroup(g, tokens)
			}

			// Get selected tools
			tools, err := tr.Tools(ctx)
			if err != nil {
				t.Fatalf("error: %v", err)
			}

			names := toolNames(tools)
			selectedGroups := detectGroups(tools)

			// Evaluate quality
			verdict := "PASS"
			var issues []string

			// Check must-have tools
			for _, must := range tc.mustHaveTools {
				if !hasToolNamed(tools, must) {
					issues = append(issues, "MISSING required: "+must)
					verdict = "FAIL"
				}
			}

			// Check must-not tools (only if not fallback)
			isFallback := len(tc.expectedGroups) == 0
			if !isFallback {
				for _, mustNot := range tc.mustNotTools {
					if hasToolNamed(tools, mustNot) {
						issues = append(issues, "UNWANTED included: "+mustNot)
						if verdict != "FAIL" {
							verdict = "WARN"
						}
					}
				}
			}

			// Check expected groups
			if len(tc.expectedGroups) > 0 {
				for _, eg := range tc.expectedGroups {
					if !containsStr(selectedGroups, eg) {
						issues = append(issues, "MISSING group: "+eg)
						verdict = "FAIL"
					}
				}
			} else {
				// Expect fallback
				if len(tools) != 16 {
					issues = append(issues, fmt.Sprintf("Expected fallback (16 tools), got %d", len(tools)))
					if verdict != "FAIL" {
						verdict = "WARN"
					}
				}
			}

			// Check max tools
			if tc.maxTools > 0 && len(tools) > tc.maxTools {
				issues = append(issues, fmt.Sprintf("Too many tools: %d > %d", len(tools), tc.maxTools))
				if verdict != "FAIL" {
					verdict = "WARN"
				}
			}

			// Count result
			switch verdict {
			case "PASS":
				passed++
			case "WARN":
				warned++
			case "FAIL":
				failed++
			}

			// Print report
			t.Logf("")
			t.Logf("  Query:    %q", tc.query)
			t.Logf("  Tokens:   %v", tokens)
			t.Logf("  Scores:   plant=%.1f  device=%.1f  support=%.1f  (threshold=%.1f)",
				scores["plant"], scores["device"], scores["support"], tr.threshold)
			t.Logf("  Groups:   %v", selectedGroups)
			t.Logf("  Tools:    %d → %v", len(tools), names)
			t.Logf("  Savings:  %d/16 tools filtered = %.0f%% token savings",
				16-len(tools), float64(16-len(tools))/16*100)

			if len(issues) > 0 {
				for _, iss := range issues {
					t.Logf("  Issue:    %s", iss)
				}
			}

			t.Logf("  Verdict:  [%s]", verdict)

			if verdict == "FAIL" {
				t.Errorf("[%s] %s — routing quality FAIL: %v", tc.name, tc.query, issues)
			}
		})
	}

	t.Logf("%s", "")
	t.Logf("%s", "====================================================================================================")
	t.Logf("  SUMMARY: %d PASS / %d WARN / %d FAIL  (out of %d)",
		passed, warned, failed, len(cases))
	t.Logf("%s", "====================================================================================================")

	if failed > 0 {
		t.Errorf("%d quality tests FAILED", failed)
	}
}

// detectGroups determines which tool groups are represented in the selection.
func detectGroups(tools []tool.Tool) []string {
	plantTools := map[string]bool{
		"search_plant": true, "get_plant_categories": true,
		"get_plants_by_category": true, "get_plant_care": true,
		"compare_plants": true, "recommend_plants": true,
	}
	deviceTools := map[string]bool{
		"get_user_devices": true, "get_sensor_reading": true,
		"get_setup_guide": true, "troubleshoot_device": true,
		"get_mesh_info": true,
	}
	supportTools := map[string]bool{
		"search_faq": true, "get_app_info": true,
		"get_contact_info": true, "get_feature_guide": true,
		"get_security_info": true,
	}

	hasPlant, hasDevice, hasSupport := false, false, false
	for _, t := range tools {
		n := t.Name()
		if plantTools[n] {
			hasPlant = true
		}
		if deviceTools[n] {
			hasDevice = true
		}
		if supportTools[n] {
			hasSupport = true
		}
	}

	var groups []string
	if hasPlant {
		groups = append(groups, "plant")
	}
	if hasDevice {
		groups = append(groups, "device")
	}
	if hasSupport {
		groups = append(groups, "support")
	}
	return groups
}

func containsStr(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
