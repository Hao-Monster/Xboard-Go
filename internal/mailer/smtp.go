package mailer

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

const (
	EncryptionStartTLS = "starttls"
	EncryptionTLS      = "tls"
	EncryptionNone     = "none"
	maxMailBodyBytes   = 128 << 10
)

type SMTPConfig struct {
	Host        string
	Port        int
	Username    string
	Password    string
	Encryption  string
	FromAddress string
}

type Message struct {
	To      string
	Subject string
	Text    string
}

type Sender interface {
	Send(context.Context, SMTPConfig, Message) error
}

type SMTPSender struct {
	timeout       time.Duration
	allowInsecure bool
}

func NewSMTPSender(timeout time.Duration, allowInsecure bool) *SMTPSender {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &SMTPSender{timeout: timeout, allowInsecure: allowInsecure}
}

func (sender *SMTPSender) Send(ctx context.Context, configuration SMTPConfig, message Message) error {
	configuration.Host = strings.TrimSpace(configuration.Host)
	configuration.Encryption = strings.ToLower(strings.TrimSpace(configuration.Encryption))
	configuration.FromAddress = strings.TrimSpace(configuration.FromAddress)
	message.To = strings.TrimSpace(message.To)
	message.Subject = strings.TrimSpace(message.Subject)
	if sender == nil || configuration.Host == "" || strings.ContainsAny(configuration.Host, "\r\n") || configuration.Port < 1 || configuration.Port > 65_535 ||
		(configuration.Encryption != EncryptionStartTLS && configuration.Encryption != EncryptionTLS && configuration.Encryption != EncryptionNone) {
		return errors.New("invalid SMTP transport configuration")
	}
	if configuration.Encryption == EncryptionNone && !sender.allowInsecure {
		return errors.New("cleartext SMTP is disabled")
	}
	if configuration.Encryption == EncryptionNone && configuration.Username != "" {
		return errors.New("SMTP authentication over cleartext is disabled")
	}
	from, err := parseEnvelopeAddress(configuration.FromAddress)
	if err != nil {
		return fmt.Errorf("invalid SMTP sender: %w", err)
	}
	to, err := parseEnvelopeAddress(message.To)
	if err != nil {
		return fmt.Errorf("invalid SMTP recipient: %w", err)
	}
	if message.Subject == "" || containsHeaderControl(message.Subject) || len(message.Text) > maxMailBodyBytes {
		return errors.New("invalid email message")
	}

	deadline := time.Now().Add(sender.timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	dialer := &net.Dialer{Timeout: sender.timeout, Deadline: deadline}
	address := net.JoinHostPort(configuration.Host, strconv.Itoa(configuration.Port))
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: configuration.Host}
	var connection net.Conn
	if configuration.Encryption == EncryptionTLS {
		connection, err = (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(ctx, "tcp", address)
	} else {
		connection, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return fmt.Errorf("connect to SMTP server: %w", err)
	}
	defer connection.Close()
	if err := connection.SetDeadline(deadline); err != nil {
		return fmt.Errorf("set SMTP deadline: %w", err)
	}
	client, err := smtp.NewClient(connection, configuration.Host)
	if err != nil {
		return fmt.Errorf("initialize SMTP client: %w", err)
	}
	defer client.Close()
	if configuration.Encryption == EncryptionStartTLS {
		if supported, _ := client.Extension("STARTTLS"); !supported {
			return errors.New("SMTP server does not advertise STARTTLS")
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("start SMTP TLS: %w", err)
		}
	}
	if configuration.Username != "" {
		if configuration.Password == "" {
			return errors.New("SMTP password is required when a username is configured")
		}
		if supported, _ := client.Extension("AUTH"); !supported {
			return errors.New("SMTP server does not advertise authentication")
		}
		if err := client.Auth(smtp.PlainAuth("", configuration.Username, configuration.Password, configuration.Host)); err != nil {
			return fmt.Errorf("authenticate to SMTP server: %w", err)
		}
	}
	if err := client.Mail(from.Address); err != nil {
		return fmt.Errorf("set SMTP sender: %w", err)
	}
	if err := client.Rcpt(to.Address); err != nil {
		return fmt.Errorf("set SMTP recipient: %w", err)
	}
	data, err := client.Data()
	if err != nil {
		return fmt.Errorf("start SMTP message: %w", err)
	}
	payload, err := buildMessage(from, to, message)
	if err == nil {
		_, err = io.Copy(data, strings.NewReader(payload))
	}
	closeErr := data.Close()
	if err != nil {
		return fmt.Errorf("write SMTP message: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("finish SMTP message: %w", closeErr)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("finish SMTP session: %w", err)
	}
	return nil
}

func parseEnvelopeAddress(value string) (*mail.Address, error) {
	if containsHeaderControl(value) {
		return nil, errors.New("address contains header controls")
	}
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address == "" || containsHeaderControl(address.Address) {
		return nil, errors.New("address is malformed")
	}
	return address, nil
}

func buildMessage(from, to *mail.Address, message Message) (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate email message id: %w", err)
	}
	var result strings.Builder
	writer := bufio.NewWriter(&result)
	headers := []string{
		"Date: " + time.Now().UTC().Format(time.RFC1123Z),
		"Message-ID: <" + hex.EncodeToString(random) + "@xboard-go.local>",
		"From: " + from.String(),
		"To: " + to.String(),
		"Subject: " + mime.QEncoding.Encode("UTF-8", message.Subject),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: 8bit",
	}
	for _, header := range headers {
		if _, err := writer.WriteString(header + "\r\n"); err != nil {
			return "", err
		}
	}
	if _, err := writer.WriteString("\r\n" + normalizeCRLF(message.Text)); err != nil {
		return "", err
	}
	if err := writer.Flush(); err != nil {
		return "", err
	}
	return result.String(), nil
}

func containsHeaderControl(value string) bool {
	return strings.ContainsAny(value, "\r\n")
}

func normalizeCRLF(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.ReplaceAll(value, "\n", "\r\n")
}
