package llm

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// WebsiteInspector инспектирует веб-страницы для извлечения структуры и UI-элементов
type WebsiteInspector struct {
	baseURL string
	client  *http.Client
}

// NewWebsiteInspector создает новый инспектор сайта
func NewWebsiteInspector(baseURL string) *WebsiteInspector {
	return &WebsiteInspector{
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// InspectPage получает и парсит страницу сайта
func (w *WebsiteInspector) InspectPage(ctx context.Context, pagePath string) (string, error) {
	// Нормализуем путь
	if pagePath == "" {
		pagePath = "/"
	}
	if !strings.HasPrefix(pagePath, "/") {
		pagePath = "/" + pagePath
	}

	url := w.baseURL + pagePath
	log.Printf("[WEBSITE_INSPECTOR] Inspecting page: %s", url)

	// Создаем запрос
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Добавляем заголовки чтобы выглядеть как браузер
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; EnddelbotBot/1.0)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en;q=0.8")

	// Выполняем запрос
	resp, err := w.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch page: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("page returned status %d", resp.StatusCode)
	}

	// Читаем HTML
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	// Парсим HTML
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("failed to parse HTML: %w", err)
	}

	// Извлекаем важную информацию
	info := w.extractPageInfo(doc, pagePath)

	log.Printf("[WEBSITE_INSPECTOR] Successfully inspected %s, extracted %d chars", url, len(info))
	return info, nil
}

// extractPageInfo извлекает структурированную информацию со страницы
func (w *WebsiteInspector) extractPageInfo(doc *html.Node, pagePath string) string {
	var result strings.Builder

	result.WriteString(fmt.Sprintf("📄 Page: %s%s\n\n", w.baseURL, pagePath))

	// Извлекаем заголовок страницы
	if title := w.findTitle(doc); title != "" {
		result.WriteString(fmt.Sprintf("Page Title: %s\n\n", title))
	}

	// Извлекаем meta description
	if desc := w.findMetaDescription(doc); desc != "" {
		result.WriteString(fmt.Sprintf("Description: %s\n\n", desc))
	}

	// Извлекаем основные заголовки (h1, h2)
	headings := w.findHeadings(doc)
	if len(headings) > 0 {
		result.WriteString("Main Headings:\n")
		for _, h := range headings {
			result.WriteString(fmt.Sprintf("  • %s\n", h))
		}
		result.WriteString("\n")
	}

	// Извлекаем кнопки и ссылки
	buttons := w.findButtons(doc)
	if len(buttons) > 0 {
		result.WriteString("Buttons & Actions:\n")
		for _, btn := range buttons {
			result.WriteString(fmt.Sprintf("  • %s\n", btn))
		}
		result.WriteString("\n")
	}

	// Извлекаем формы
	forms := w.findForms(doc)
	if len(forms) > 0 {
		result.WriteString("Forms:\n")
		for _, form := range forms {
			result.WriteString(fmt.Sprintf("  %s\n", form))
		}
		result.WriteString("\n")
	}

	// Извлекаем навигацию
	navLinks := w.findNavigation(doc)
	if len(navLinks) > 0 {
		result.WriteString("Navigation Links:\n")
		for _, link := range navLinks {
			result.WriteString(fmt.Sprintf("  • %s\n", link))
		}
		result.WriteString("\n")
	}

	// Извлекаем текстовый контент (ограниченно)
	mainText := w.findMainContent(doc)
	if mainText != "" {
		result.WriteString("Main Content Preview:\n")
		// Ограничиваем до 500 символов
		if len(mainText) > 500 {
			mainText = mainText[:500] + "..."
		}
		result.WriteString(mainText)
		result.WriteString("\n\n")
	}

	return result.String()
}

// findTitle находит тег <title>
func (w *WebsiteInspector) findTitle(n *html.Node) string {
	if n.Type == html.ElementNode && n.Data == "title" {
		return w.getNodeText(n)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if title := w.findTitle(c); title != "" {
			return title
		}
	}
	return ""
}

// findMetaDescription находит meta description
func (w *WebsiteInspector) findMetaDescription(n *html.Node) string {
	if n.Type == html.ElementNode && n.Data == "meta" {
		var name, content string
		for _, attr := range n.Attr {
			if attr.Key == "name" && attr.Val == "description" {
				name = attr.Val
			}
			if attr.Key == "content" {
				content = attr.Val
			}
		}
		if name != "" && content != "" {
			return content
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if desc := w.findMetaDescription(c); desc != "" {
			return desc
		}
	}
	return ""
}

// findHeadings находит заголовки h1-h3
func (w *WebsiteInspector) findHeadings(n *html.Node) []string {
	var headings []string
	w.walkNode(n, func(node *html.Node) {
		if node.Type == html.ElementNode {
			switch node.Data {
			case "h1", "h2", "h3":
				text := strings.TrimSpace(w.getNodeText(node))
				if text != "" && len(text) < 200 {
					headings = append(headings, fmt.Sprintf("[%s] %s", strings.ToUpper(node.Data), text))
				}
			}
		}
	})
	return headings
}

// findButtons находит кнопки и важные ссылки
func (w *WebsiteInspector) findButtons(n *html.Node) []string {
	var buttons []string
	seen := make(map[string]bool)

	w.walkNode(n, func(node *html.Node) {
		if node.Type == html.ElementNode {
			var btnText string
			switch node.Data {
			case "button":
				btnText = strings.TrimSpace(w.getNodeText(node))
			case "a":
				// Извлекаем только важные ссылки (с классами типа btn, button, или с определенным текстом)
				hasButtonClass := false
				for _, attr := range node.Attr {
					if attr.Key == "class" && (strings.Contains(attr.Val, "btn") || strings.Contains(attr.Val, "button")) {
						hasButtonClass = true
						break
					}
				}
				if hasButtonClass {
					btnText = strings.TrimSpace(w.getNodeText(node))
				}
			case "input":
				// Кнопки submit
				var inputType string
				for _, attr := range node.Attr {
					if attr.Key == "type" {
						inputType = attr.Val
					}
					if attr.Key == "value" && (inputType == "submit" || inputType == "button") {
						btnText = attr.Val
					}
				}
			}

			if btnText != "" && len(btnText) < 100 && !seen[btnText] {
				buttons = append(buttons, btnText)
				seen[btnText] = true
			}
		}
	})

	return buttons
}

// findForms находит формы на странице
func (w *WebsiteInspector) findForms(n *html.Node) []string {
	var forms []string
	w.walkNode(n, func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "form" {
			formDesc := w.describeForm(node)
			if formDesc != "" {
				forms = append(forms, formDesc)
			}
		}
	})
	return forms
}

// describeForm описывает форму и её поля
func (w *WebsiteInspector) describeForm(formNode *html.Node) string {
	var fields []string
	w.walkNode(formNode, func(node *html.Node) {
		if node.Type == html.ElementNode {
			switch node.Data {
			case "input":
				var inputType, name, placeholder string
				for _, attr := range node.Attr {
					if attr.Key == "type" {
						inputType = attr.Val
					}
					if attr.Key == "name" {
						name = attr.Val
					}
					if attr.Key == "placeholder" {
						placeholder = attr.Val
					}
				}
				if inputType != "" && inputType != "hidden" {
					fieldDesc := inputType
					if name != "" {
						fieldDesc += " (" + name + ")"
					}
					if placeholder != "" {
						fieldDesc += ": " + placeholder
					}
					fields = append(fields, fieldDesc)
				}
			case "textarea":
				var name, placeholder string
				for _, attr := range node.Attr {
					if attr.Key == "name" {
						name = attr.Val
					}
					if attr.Key == "placeholder" {
						placeholder = attr.Val
					}
				}
				fieldDesc := "textarea"
				if name != "" {
					fieldDesc += " (" + name + ")"
				}
				if placeholder != "" {
					fieldDesc += ": " + placeholder
				}
				fields = append(fields, fieldDesc)
			}
		}
	})

	if len(fields) == 0 {
		return ""
	}

	return "Form with fields: " + strings.Join(fields, ", ")
}

// findNavigation находит навигационные ссылки
func (w *WebsiteInspector) findNavigation(n *html.Node) []string {
	var links []string
	seen := make(map[string]bool)

	w.walkNode(n, func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "nav" {
			w.walkNode(node, func(linkNode *html.Node) {
				if linkNode.Type == html.ElementNode && linkNode.Data == "a" {
					text := strings.TrimSpace(w.getNodeText(linkNode))
					var href string
					for _, attr := range linkNode.Attr {
						if attr.Key == "href" {
							href = attr.Val
						}
					}
					if text != "" && !seen[text] && len(text) < 100 {
						if href != "" && href != "#" {
							links = append(links, fmt.Sprintf("%s (%s)", text, href))
						} else {
							links = append(links, text)
						}
						seen[text] = true
					}
				}
			})
		}
	})

	return links
}

// findMainContent находит основной текстовый контент
func (w *WebsiteInspector) findMainContent(n *html.Node) string {
	var mainContent strings.Builder

	// Ищем main, article или div с классом content/main
	w.walkNode(n, func(node *html.Node) {
		if node.Type == html.ElementNode {
			isMainContent := false
			switch node.Data {
			case "main", "article":
				isMainContent = true
			case "div":
				for _, attr := range node.Attr {
					if attr.Key == "class" && (strings.Contains(attr.Val, "content") || strings.Contains(attr.Val, "main")) {
						isMainContent = true
						break
					}
				}
			}

			if isMainContent {
				// Извлекаем текст из параграфов
				w.walkNode(node, func(pNode *html.Node) {
					if pNode.Type == html.ElementNode && pNode.Data == "p" {
						text := strings.TrimSpace(w.getNodeText(pNode))
						if text != "" && len(text) > 20 { // Игнорируем короткие строки
							mainContent.WriteString(text)
							mainContent.WriteString("\n\n")
						}
					}
				})
			}
		}
	})

	return strings.TrimSpace(mainContent.String())
}

// getNodeText извлекает весь текст из узла
func (w *WebsiteInspector) getNodeText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var text strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		text.WriteString(w.getNodeText(c))
	}
	return cleanText(text.String())
}

// walkNode обходит дерево DOM
func (w *WebsiteInspector) walkNode(n *html.Node, f func(*html.Node)) {
	f(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		w.walkNode(c, f)
	}
}

// cleanText очищает текст от лишних пробелов и переносов
func cleanText(s string) string {
	// Убираем множественные пробелы
	space := regexp.MustCompile(`\s+`)
	s = space.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}
