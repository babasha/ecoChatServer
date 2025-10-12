package main

import (
	"fmt"
	"os"

	"golang.org/x/crypto/bcrypt"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: go run create_admin.go <email> <password>")
		fmt.Println("Example: go run create_admin.go superadmin@ecochat.com MySecurePass123")
		os.Exit(1)
	}

	email := os.Args[1]
	password := os.Args[2]

	// Генерируем bcrypt hash
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		fmt.Printf("Error generating hash: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=== SQL для создания администратора ===")
	fmt.Println()
	fmt.Printf("INSERT INTO users (email, email_verified, display_name, password_hash, role_id, status)\n")
	fmt.Printf("VALUES (\n")
	fmt.Printf("  '%s',\n", email)
	fmt.Printf("  true,\n")
	fmt.Printf("  'Administrator',\n")
	fmt.Printf("  '%s',\n", string(hash))
	fmt.Printf("  (SELECT id FROM roles WHERE name='super_admin'),\n")
	fmt.Printf("  'active'\n")
	fmt.Printf(");\n")
	fmt.Println()
	fmt.Println("=== Или используйте этот хеш пароля напрямую ===")
	fmt.Printf("Password hash: %s\n", string(hash))
}
