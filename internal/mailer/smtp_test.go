package mailer

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestSMTPSenderDeliversUTF8MessageAndRejectsUnsafeTransport(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	data := make(chan string, 1)
	serverError := make(chan error, 1)
	go serveTestSMTP(listener, data, serverError)

	port := listener.Addr().(*net.TCPAddr).Port
	configuration := SMTPConfig{
		Host: "127.0.0.1", Port: port, Encryption: EncryptionNone,
		FromAddress: "support@example.test",
	}
	message := Message{To: "user@example.test", Subject: "工单得到了回复", Text: "主题：连接问题\r\n回复内容：请重试"}

	secureSender := NewSMTPSender(3*time.Second, false)
	if err := secureSender.Send(context.Background(), configuration, message); err == nil {
		t.Fatal("Send() accepted cleartext SMTP without an explicit local-test override")
	}

	sender := NewSMTPSender(3*time.Second, true)
	if err := sender.Send(context.Background(), configuration, message); err != nil {
		t.Fatal(err)
	}
	select {
	case payload := <-data:
		if !strings.Contains(payload, "Subject: =?UTF-8?") || !strings.Contains(payload, "user@example.test") || !strings.Contains(payload, "主题：连接问题") {
			t.Fatalf("SMTP payload is incomplete:\n%s", payload)
		}
	case err := <-serverError:
		t.Fatal(err)
	case <-time.After(3 * time.Second):
		t.Fatal("test SMTP server did not receive a message")
	}

	if err := sender.Send(context.Background(), configuration, Message{To: "user@example.test\r\nBcc: attacker@example.test", Subject: "unsafe", Text: "body"}); err == nil {
		t.Fatal("Send() accepted a header-injection recipient")
	}
}

func serveTestSMTP(listener net.Listener, data chan<- string, result chan<- error) {
	connection, err := listener.Accept()
	if err != nil {
		result <- err
		return
	}
	defer connection.Close()
	reader := bufio.NewReader(connection)
	writer := bufio.NewWriter(connection)
	write := func(value string) error {
		if _, err := writer.WriteString(value + "\r\n"); err != nil {
			return err
		}
		return writer.Flush()
	}
	if err := write("220 localhost test SMTP"); err != nil {
		result <- err
		return
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			result <- err
			return
		}
		switch command := strings.ToUpper(strings.TrimSpace(line)); {
		case strings.HasPrefix(command, "EHLO"):
			if err := write("250-localhost\r\n250 SIZE 131072"); err != nil {
				result <- err
				return
			}
		case strings.HasPrefix(command, "MAIL FROM:"), strings.HasPrefix(command, "RCPT TO:"):
			if err := write("250 accepted"); err != nil {
				result <- err
				return
			}
		case command == "DATA":
			if err := write("354 end with dot"); err != nil {
				result <- err
				return
			}
			var payload strings.Builder
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					result <- err
					return
				}
				if line == ".\r\n" {
					break
				}
				payload.WriteString(line)
			}
			if err := write("250 queued"); err != nil {
				result <- err
				return
			}
			data <- payload.String()
		case command == "QUIT":
			_ = write("221 bye")
			return
		default:
			result <- fmt.Errorf("unexpected SMTP command %q", strings.TrimSpace(line))
			return
		}
	}
}
