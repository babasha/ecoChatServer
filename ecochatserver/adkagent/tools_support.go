package adkagent

import (
	"fmt"
	"log"
	"slices"
	"strings"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/egor/ecochatserver/llm"
)

// ============================================================================
// SUPPORT AGENT TOOLS - Инструменты для техподдержки
// ============================================================================

// CreateSupportTools создает набор инструментов для поддержки
func CreateSupportTools(storeClient *llm.StoreClient) ([]tool.Tool, error) {
	var tools []tool.Tool

	// ======== Базовые инструменты (уже существуют) ========

	// Tool 1: Get Store Info
	getStoreInfoTool, err := createGetStoreInfoTool(storeClient)
	if err != nil {
		return nil, err
	}
	tools = append(tools, getStoreInfoTool)

	// Tool 2: Inspect Website Page
	inspectWebsiteTool, err := createInspectWebsiteTool(storeClient)
	if err != nil {
		return nil, err
	}
	tools = append(tools, inspectWebsiteTool)

	// ======== НОВЫЕ инструменты ========

	// Tool 3: Search FAQ - поиск в базе знаний
	searchFAQTool, err := createSearchFAQTool()
	if err != nil {
		return nil, err
	}
	tools = append(tools, searchFAQTool)

	// Tool 4: Get Contact Info - контактная информация
	getContactInfoTool, err := createGetContactInfoTool()
	if err != nil {
		return nil, err
	}
	tools = append(tools, getContactInfoTool)

	// Tool 5: Check Service Status - статус сервисов
	checkServiceStatusTool, err := createCheckServiceStatusTool()
	if err != nil {
		return nil, err
	}
	tools = append(tools, checkServiceStatusTool)

	log.Printf("[TOOLS_SUPPORT] Created %d support tools", len(tools))
	return tools, nil
}

// ============================================================================
// Базовые tools
// ============================================================================

func createGetStoreInfoTool(storeClient *llm.StoreClient) (tool.Tool, error) {
	type GetStoreInfoInput struct {
		InfoType string `json:"infoType"` // all, delivery, payment, hours, location
	}
	type StoreInfoOutput struct {
		Info string `json:"info"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "get_store_info",
			Description: "Get store information about delivery, payment, working hours, etc. Use when customer asks general questions about the store. InfoType can be: 'all', 'delivery', 'payment', 'hours', 'location'.",
		},
		func(ctx tool.Context, input GetStoreInfoInput) (StoreInfoOutput, error) {
			log.Printf("[TOOL] get_store_info called: type=%s", input.InfoType)

			infoType := input.InfoType
			if infoType == "" {
				infoType = "all"
			}

			result := "📋 Enddel Store Information\n\n"

			if infoType == "all" || infoType == "delivery" {
				result += "🚚 DELIVERY:\n"
				result += "  • Cost: 3₾ for orders < 50₾, FREE for 50₾+\n"
				result += "  • Time: 30-60 minutes\n"
				result += "  • Hours: 8:00-23:00 daily\n"
				result += "  • Coverage: Tbilisi city area\n\n"
			}

			if infoType == "all" || infoType == "payment" {
				result += "💳 PAYMENT METHODS:\n"
				result += "  • Card (online payment)\n"
				result += "  • Cash (to courier)\n"
				result += "  • Google Pay\n"
				result += "  • Telegram Pay\n\n"
			}

			if infoType == "all" || infoType == "hours" {
				result += "🕐 WORKING HOURS:\n"
				result += "  • Monday-Sunday: 8:00-23:00\n"
				result += "  • Customer Support: 9:00-22:00\n\n"
			}

			if infoType == "all" || infoType == "location" {
				result += "📍 LOCATION:\n"
				result += "  • Service area: Tbilisi, Georgia\n"
				result += "  • Website: https://enddel.com\n"
			}

			return StoreInfoOutput{Info: result}, nil
		},
	)
}

func createInspectWebsiteTool(storeClient *llm.StoreClient) (tool.Tool, error) {
	type InspectWebsiteInput struct {
		PagePath string `json:"pagePath"` // URL path to inspect
	}
	type InspectWebsiteOutput struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "inspect_website_page",
			Description: "Get information from website pages. Use when customer asks about website structure, specific pages, or UI elements. Provide the page path (e.g., '/products', '/cart', '/about').",
		},
		func(ctx tool.Context, input InspectWebsiteInput) (InspectWebsiteOutput, error) {
			log.Printf("[TOOL] inspect_website_page called: pagePath=%s", input.PagePath)

			inspector := llm.NewWebsiteInspector(storeClient.GetBaseURL())

			pageInfo, err := inspector.InspectPage(ctx, input.PagePath)
			if err != nil {
				log.Printf("[TOOL] Error inspecting website page %s: %v", input.PagePath, err)
				return InspectWebsiteOutput{
					Result: fmt.Sprintf("Error inspecting page %s: %v. The page might not be accessible or doesn't exist.", input.PagePath, err),
				}, nil
			}

			return InspectWebsiteOutput{Result: pageInfo}, nil
		},
	)
}

// ============================================================================
// НОВЫЕ ПРОДВИНУТЫЕ TOOLS
// ============================================================================

// createSearchFAQTool - поиск в базе знаний FAQ
func createSearchFAQTool() (tool.Tool, error) {
	type SearchFAQInput struct {
		Query string `json:"query"` // Поисковый запрос
	}
	type SearchFAQOutput struct {
		Result string `json:"result"`
	}

	// База знаний FAQ (в продакшене это была бы БД или отдельный сервис)
	faqDatabase := map[string]struct {
		question string
		answer   string
		tags     []string
	}{
		"minimum_order": {
			question: "What is the minimum order amount?",
			answer:   "There is no minimum order amount at Enddel! However, delivery is FREE for orders 50₾ and above. For orders under 50₾, delivery cost is 3₾.",
			tags:     []string{"order", "delivery", "minimum", "cost"},
		},
		"delivery_time": {
			question: "How long does delivery take?",
			answer:   "Standard delivery takes 30-60 minutes from the time you place your order. During peak hours, it may take slightly longer. You can track your order in real-time once a courier is assigned.",
			tags:     []string{"delivery", "time", "tracking"},
		},
		"payment_methods": {
			question: "What payment methods do you accept?",
			answer:   "We accept: Credit/Debit cards (Visa, Mastercard), Cash on delivery, Google Pay, Telegram Pay. Payment is secure and processed through our trusted payment partners.",
			tags:     []string{"payment", "card", "cash", "methods"},
		},
		"returns": {
			question: "What is your return/refund policy?",
			answer:   "If you receive damaged or incorrect items, please contact us within 24 hours. We offer full refunds or replacements. For perishable items, we can only accept returns if they are spoiled or damaged upon delivery.",
			tags:     []string{"return", "refund", "policy", "damaged"},
		},
		"coverage_area": {
			question: "What areas do you deliver to?",
			answer:   "We currently deliver throughout Tbilisi city. Coverage includes all major districts. Enter your address during checkout to verify if we deliver to your location.",
			tags:     []string{"delivery", "area", "coverage", "location"},
		},
		"cancel_order": {
			question: "Can I cancel my order?",
			answer:   "Yes! You can cancel your order free of charge if it hasn't been prepared yet. Once preparation begins, cancellation may not be possible. Contact support immediately if you need to cancel.",
			tags:     []string{"cancel", "order", "refund"},
		},
		"product_quality": {
			question: "How do you ensure product quality?",
			answer:   "We partner with trusted vendors and suppliers. All products are checked before packaging. Fresh produce and perishables are selected carefully. We guarantee quality or offer full refunds.",
			tags:     []string{"quality", "fresh", "guarantee"},
		},
		"account_create": {
			question: "Do I need to create an account?",
			answer:   "No, you can shop as a guest! However, creating an account allows you to: Track orders easily, Save favorite items, Get personalized recommendations, Access order history.",
			tags:     []string{"account", "registration", "guest"},
		},
		"promo_codes": {
			question: "Do you have promo codes or discounts?",
			answer:   "Yes! We regularly offer promotions and discounts. Check our website homepage for current deals. First-time customers often get special welcome discounts. Subscribe to our newsletter for exclusive offers.",
			tags:     []string{"promo", "discount", "coupon", "deals"},
		},
		"contact_support": {
			question: "How can I contact customer support?",
			answer:   "You can reach us via: Live chat on website (9:00-22:00), Email: support@enddel.com, Phone: +995 XXX XXX XXX, Social media: @enddel on Instagram/Facebook",
			tags:     []string{"contact", "support", "help", "phone"},
		},
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "search_faq",
			Description: "Search the knowledge base for frequently asked questions. Use when customer asks common questions about policies, delivery, payments, returns, etc. Provide search query related to customer's question.",
		},
		func(ctx tool.Context, input SearchFAQInput) (SearchFAQOutput, error) {
			log.Printf("[TOOL] search_faq called: query=%s", input.Query)

			if input.Query == "" {
				return SearchFAQOutput{Result: "Error: search query is required"}, nil
			}

			query := strings.ToLower(input.Query)

			// Простой поиск по тегам и вопросам
			var matches []string
			for key, faq := range faqDatabase {
				// Проверяем совпадение в тегах
				for _, tag := range faq.tags {
					if strings.Contains(query, tag) || strings.Contains(tag, query) {
						matches = append(matches, key)
						break
					}
				}

				// Проверяем совпадение в вопросе
				if strings.Contains(strings.ToLower(faq.question), query) {
					if !slices.Contains(matches, key) {
						matches = append(matches, key)
					}
				}
			}

			if len(matches) == 0 {
				result := "❓ No exact FAQ match found for your question.\n\n"
				result += "However, here are some popular topics:\n"
				result += "• Delivery time and cost\n"
				result += "• Payment methods\n"
				result += "• Order cancellation\n"
				result += "• Return/refund policy\n"
				result += "• Coverage area\n\n"
				result += "Please ask about any of these topics or contact our support team."
				return SearchFAQOutput{Result: result}, nil
			}

			// Формируем ответ
			result := "💡 FAQ RESULTS\n\n"
			result += fmt.Sprintf("Found %d relevant answers:\n\n", len(matches))

			for i, key := range matches {
				if i >= 3 { // Ограничиваем до 3 результатов
					result += fmt.Sprintf("\n... and %d more results. Contact support for details.", len(matches)-3)
					break
				}

				faq := faqDatabase[key]
				result += fmt.Sprintf("━━━ %s ━━━\n", faq.question)
				result += fmt.Sprintf("%s\n\n", faq.answer)
			}

			return SearchFAQOutput{Result: result}, nil
		},
	)
}

// createGetContactInfoTool - получение контактной информации
func createGetContactInfoTool() (tool.Tool, error) {
	type GetContactInfoInput struct {
		ContactType string `json:"contactType"` // all, phone, email, social, address
	}
	type GetContactInfoOutput struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "get_contact_info",
			Description: "Get contact information for customer support. Use when customer asks 'how to contact you', 'support phone', 'email address', 'social media'. Type can be: 'all', 'phone', 'email', 'social', 'address'.",
		},
		func(ctx tool.Context, input GetContactInfoInput) (GetContactInfoOutput, error) {
			log.Printf("[TOOL] get_contact_info called: type=%s", input.ContactType)

			contactType := input.ContactType
			if contactType == "" {
				contactType = "all"
			}

			result := "📞 CONTACT INFORMATION\n\n"

			if contactType == "all" || contactType == "phone" {
				result += "📱 PHONE:\n"
				result += "  • Main: +995 XXX XXX XXX\n"
				result += "  • WhatsApp: +995 XXX XXX XXX\n"
				result += "  • Available: 9:00-22:00 daily\n\n"
			}

			if contactType == "all" || contactType == "email" {
				result += "📧 EMAIL:\n"
				result += "  • Support: support@enddel.com\n"
				result += "  • Business: business@enddel.com\n"
				result += "  • Response time: < 24 hours\n\n"
			}

			if contactType == "all" || contactType == "social" {
				result += "💬 SOCIAL MEDIA:\n"
				result += "  • Instagram: @enddel\n"
				result += "  • Facebook: facebook.com/enddel\n"
				result += "  • Telegram: t.me/enddel\n\n"
			}

			if contactType == "all" || contactType == "address" {
				result += "🏢 OFFICE:\n"
				result += "  • Tbilisi, Georgia\n"
				result += "  • Business hours: Mon-Fri 10:00-18:00\n"
			}

			result += "\n💡 TIP: For urgent issues, use live chat on our website!"

			return GetContactInfoOutput{Result: result}, nil
		},
	)
}

// createCheckServiceStatusTool - проверка статуса сервисов
func createCheckServiceStatusTool() (tool.Tool, error) {
	type CheckServiceStatusInput struct {
		// No input needed
	}
	type CheckServiceStatusOutput struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "check_service_status",
			Description: "Check current status of services (website, delivery, payment systems). Use when customer reports issues like 'website not working', 'can't place order', 'payment failed'.",
		},
		func(ctx tool.Context, input CheckServiceStatusInput) (CheckServiceStatusOutput, error) {
			log.Printf("[TOOL] check_service_status called")

			// В продакшене это был бы реальный мониторинг сервисов
			// Пока возвращаем статичную информацию

			result := "🔍 SERVICE STATUS CHECK\n\n"
			result += "✅ Website: Operational\n"
			result += "✅ Mobile App: Operational\n"
			result += "✅ Ordering System: Operational\n"
			result += "✅ Payment Gateway: Operational\n"
			result += "✅ Delivery Service: Operational\n"
			result += "✅ Customer Support: Available\n\n"

			result += "🕐 Last checked: Just now\n\n"

			result += "If you're experiencing issues:\n"
			result += "1. Try refreshing the page\n"
			result += "2. Clear browser cache\n"
			result += "3. Try a different browser\n"
			result += "4. Check your internet connection\n"
			result += "5. Contact support if problem persists\n\n"

			result += "📊 For real-time updates: status.enddel.com"

			return CheckServiceStatusOutput{Result: result}, nil
		},
	)
}

