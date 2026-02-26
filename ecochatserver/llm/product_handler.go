package llm

import (
	"fmt"
	"strings"
)

// FormatProductDetails форматирует детальную информацию о товаре
// Используется когда клиент спрашивает о конкретном товаре
func FormatProductDetails(product Product) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Product: %s\n\n", product.NameRu))

	if product.Description != "" {
		sb.WriteString(fmt.Sprintf("Description: %s\n\n", product.Description))
	}

	if product.Price != "" && product.Price != "0" && product.Price != "0.00" {
		sb.WriteString(fmt.Sprintf("Price: %s₾\n", product.Price))
	}

	if product.StockQuantity > 0 || product.InStock {
		sb.WriteString(fmt.Sprintf("In stock: %d units\n", product.StockQuantity))
	} else {
		sb.WriteString("Currently out of stock\n")
	}

	sb.WriteString("\nNote: Customer asked about specific product - present ALL details naturally (price, stock, description).")

	return sb.String()
}

// FormatProductsList форматирует список продуктов для отображения
// Возвращает краткий обзор - LLM сама решит какие детали включить в ответ
func FormatProductsList(products []Product) string {
	if len(products) == 0 {
		return "Товары не найдены."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Available products (%d total):\n\n", len(products)))

	for i, p := range products {
		// Краткий формат: название, цена и статус наличия
		status := "✓ in stock"
		if p.StockQuantity == 0 && !p.InStock {
			status = "✗ out of stock"
		}

		priceStr := ""
		if p.Price != "" && p.Price != "0" && p.Price != "0.00" {
			priceStr = fmt.Sprintf(" - %s₾", p.Price)
		}

		sb.WriteString(fmt.Sprintf("%d. %s%s (%s)\n", i+1, p.NameRu, priceStr, status))
	}

	sb.WriteString("\nNote: Present this list naturally to customer. Don't mention prices/stock unless they specifically ask.")
	return sb.String()
}
