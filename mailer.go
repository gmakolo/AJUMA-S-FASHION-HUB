package main

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"
)

type SMTPMailer struct {
	address  string
	host     string
	username string
	password string
	from     string
}

func SMTPMailerFromEnv() (*SMTPMailer, error) {
	host := strings.TrimSpace(os.Getenv("AJUMA_SMTP_HOST"))
	if host == "" {
		return nil, nil
	}
	if strings.ContainsAny(host, " \t\r\n/\\") {
		return nil, fmt.Errorf("AJUMA_SMTP_HOST must be a mail server hostname")
	}
	port := strings.TrimSpace(os.Getenv("AJUMA_SMTP_PORT"))
	if port == "" {
		port = "587"
	}
	portNumber, portErr := strconv.Atoi(port)
	if portErr != nil || portNumber < 1 || portNumber > 65535 {
		return nil, fmt.Errorf("AJUMA_SMTP_PORT must be a valid port number")
	}
	user := strings.TrimSpace(os.Getenv("AJUMA_SMTP_USER"))
	password := os.Getenv("AJUMA_SMTP_PASSWORD")
	from, err := mail.ParseAddress(strings.TrimSpace(os.Getenv("AJUMA_SMTP_FROM")))
	if err != nil || from.Address == "" {
		return nil, fmt.Errorf("AJUMA_SMTP_FROM must be a valid sender address")
	}
	if user == "" || password == "" {
		return nil, fmt.Errorf("AJUMA_SMTP_USER and AJUMA_SMTP_PASSWORD are required")
	}
	return &SMTPMailer{address: net.JoinHostPort(host, port), host: host, username: user, password: password, from: from.Address}, nil
}

func (m *SMTPMailer) SendPasswordReset(to, code string) error {
	recipient, err := mail.ParseAddress(to)
	if err != nil || recipient.Address != to {
		return fmt.Errorf("invalid reset email address")
	}
	conn, err := net.DialTimeout("tcp", m.address, 10*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	client, err := smtp.NewClient(conn, m.host)
	if err != nil {
		return err
	}
	defer client.Close()
	if ok, _ := client.Extension("STARTTLS"); !ok {
		return fmt.Errorf("SMTP server does not offer STARTTLS")
	}
	if err := client.StartTLS(&tls.Config{ServerName: m.host, MinVersion: tls.VersionTLS12}); err != nil {
		return err
	}
	auth := smtp.PlainAuth("", m.username, m.password, m.host)
	if err := client.Auth(auth); err != nil {
		return err
	}
	if err := client.Mail(m.from); err != nil {
		return err
	}
	if err := client.Rcpt(recipient.Address); err != nil {
		return err
	}
	body, err := client.Data()
	if err != nil {
		return err
	}
	_, writeErr := fmt.Fprintf(body, "From: %s\r\nTo: %s\r\nSubject: Your AJ FASHION AND DESIGN recovery code\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\nSomeone requested a password reset for your AJ FASHION AND DESIGN account.\r\n\r\nYour one-time recovery code is: %s\r\n\r\nEnter this code with your email address on the Reset Password page within 30 minutes. If you did not request this, you can ignore this email.\r\n", m.from, recipient.Address, code)
	if err := body.Close(); err != nil {
		return err
	}
	if writeErr != nil {
		return writeErr
	}
	return client.Quit()
}
