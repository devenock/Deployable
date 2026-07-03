// Package mailer sends transactional emails over SMTP (stdlib net/smtp —
// no third-party client). Configured via SMTP_* env vars for a dev provider
// like Mailtrap; swapping to a production provider (e.g. Resend's SMTP
// relay) later only requires new env values, not a code change. If SMTP_HOST
// is unset, emails are logged to stdout instead of sent, so the rest of the
// app can be exercised end to end without real credentials.
package mailer

import (
	"context"
	"fmt"
	"log"
	"net/smtp"
	"os"
)

// Mailer sends an HTML email to a single recipient.
type Mailer interface {
	Send(ctx context.Context, to, subject, htmlBody string) error
}

// Config holds SMTP connection settings.
type Config struct {
	Host      string
	Port      string
	Username  string
	Password  string
	FromEmail string
	FromName  string
}

// ConfigFromEnv reads SMTP_* env vars into a Config.
func ConfigFromEnv() Config {
	return Config{
		Host:      os.Getenv("SMTP_HOST"),
		Port:      envOr("SMTP_PORT", "587"),
		Username:  os.Getenv("SMTP_USERNAME"),
		Password:  os.Getenv("SMTP_PASSWORD"),
		FromEmail: envOr("SMTP_FROM_EMAIL", "noreply@deployable.dev"),
		FromName:  envOr("SMTP_FROM_NAME", "Deployable"),
	}
}

// New returns an SMTP-backed Mailer, or a console-logging fallback if
// SMTP_HOST/SMTP_USERNAME are not set (e.g. before Mailtrap credentials are
// provisioned for local development).
func New(cfg Config) Mailer {
	if cfg.Host == "" || cfg.Username == "" {
		log.Println("mailer: SMTP_HOST/SMTP_USERNAME not set — emails will be logged to stdout instead of sent")
		return &consoleMailer{}
	}
	return &smtpMailer{cfg: cfg}
}

type smtpMailer struct {
	cfg Config
}

func (m *smtpMailer) Send(_ context.Context, to, subject, htmlBody string) error {
	addr := m.cfg.Host + ":" + m.cfg.Port
	auth := smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)

	msg := buildMIMEMessage(m.cfg.FromName, m.cfg.FromEmail, to, subject, htmlBody)

	if err := smtp.SendMail(addr, auth, m.cfg.FromEmail, []string{to}, msg); err != nil {
		return fmt.Errorf("send mail via %s: %w", addr, err)
	}
	return nil
}

func buildMIMEMessage(fromName, fromEmail, to, subject, htmlBody string) []byte {
	headers := fmt.Sprintf(
		"From: %s <%s>\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=\"UTF-8\"\r\n\r\n",
		fromName, fromEmail, to, subject,
	)
	return []byte(headers + htmlBody)
}

type consoleMailer struct{}

func (m *consoleMailer) Send(_ context.Context, to, subject, htmlBody string) error {
	log.Printf("=== EMAIL (dev fallback — SMTP not configured) ===\nTo: %s\nSubject: %s\n\n%s\n=== END EMAIL ===", to, subject, htmlBody)
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
