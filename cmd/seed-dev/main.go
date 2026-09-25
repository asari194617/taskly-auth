package main

import (
	"database/sql"
	"log"
	"os"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	dsn := os.Getenv("AUTH_DSN")
	email := strings.ToLower(strings.TrimSpace(os.Getenv("AUTH_DEV_EMAIL")))
	password := os.Getenv("AUTH_DEV_PASSWORD")
	if dsn == "" || email == "" || password == "" || os.Getenv("NODE_ENV") == "production" {
		log.Fatal("development DSN, AUTH_DEV_EMAIL, AUTH_DEV_PASSWORD required; production forbidden")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		log.Fatal(err)
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO users (email,display_name,hashed_password,email_code_allowed,is_registered) VALUES (?,?,?,0,1) ON DUPLICATE KEY UPDATE hashed_password=VALUES(hashed_password),email_code_allowed=0`, email, "Development User", string(hash))
	if err != nil {
		log.Fatal(err)
	}
	log.Print("development account ready")
}
