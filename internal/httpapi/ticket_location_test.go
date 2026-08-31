package httpapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type fixedTicketRegionResolver struct {
	wantIP string
	region string
	err    error
	calls  int
}

func (resolver *fixedTicketRegionResolver) Region(ip string) (string, error) {
	resolver.calls++
	if ip != resolver.wantIP {
		return "", errors.New("unexpected IP")
	}
	return resolver.region, resolver.err
}

func TestLegacyTicketNotificationClientIPTrustBoundary(t *testing.T) {
	for _, test := range []struct {
		name       string
		remoteAddr string
		forwarded  []string
		want       string
	}{
		{name: "direct public ignores spoofed header", remoteAddr: "192.0.2.44:443", forwarded: []string{"8.8.8.8"}, want: "192.0.2.44"},
		{name: "trusted private proxy", remoteAddr: "172.18.0.2:8080", forwarded: []string{"114.114.114.114"}, want: "114.114.114.114"},
		{name: "trusted chain strips from right", remoteAddr: "10.0.0.4:8080", forwarded: []string{"8.8.8.8, 172.18.0.3"}, want: "8.8.8.8"},
		{name: "invalid chain fails closed", remoteAddr: "10.0.0.4:8080", forwarded: []string{"8.8.8.8, invalid"}, want: "10.0.0.4"},
		{name: "duplicate headers fail closed", remoteAddr: "10.0.0.4:8080", forwarded: []string{"8.8.8.8", "1.1.1.1"}, want: "10.0.0.4"},
		{name: "ipv4 mapped peer remains IPv6", remoteAddr: "[::ffff:192.0.2.55]:443", want: "::ffff:192.0.2.55"},
		{name: "bare peer address", remoteAddr: "192.0.2.56", want: "192.0.2.56"},
		{name: "invalid peer address", remoteAddr: "invalid", want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest("POST", "/api/v1/tickets", nil)
			request.RemoteAddr = test.remoteAddr
			for _, value := range test.forwarded {
				request.Header.Add("X-Forwarded-For", value)
			}
			if got := legacyTicketNotificationClientIP(request); got != test.want {
				t.Fatalf("legacyTicketNotificationClientIP() = %q, want %q", got, test.want)
			}
		})
	}

	request := httptest.NewRequest("POST", "/api/v1/tickets", nil)
	request.RemoteAddr = "10.0.0.4:8080"
	request.Header.Set("X-Forwarded-For", strings.Repeat("1", maxTicketForwardedForBytes+1))
	if got := legacyTicketNotificationClientIP(request); got != "10.0.0.4" {
		t.Fatalf("oversized forwarded chain = %q", got)
	}
	request.Header.Set("X-Forwarded-For", strings.Repeat("8.8.8.8,", maxTicketForwardedHops)+"8.8.8.8")
	if got := legacyTicketNotificationClientIP(request); got != "10.0.0.4" {
		t.Fatalf("overlong forwarded hop chain = %q", got)
	}
}

func TestTicketNotificationLocationPreservesIPv4AndIPv6Semantics(t *testing.T) {
	resolver := &fixedTicketRegionResolver{wantIP: "114.114.114.114", region: "中国江苏省南京市"}
	api := &server{ticketRegionResolver: resolver}
	request := httptest.NewRequest("POST", "/api/v1/tickets", nil)
	request.RemoteAddr = "172.18.0.2:8080"
	request.Header.Set("X-Forwarded-For", "114.114.114.114")
	if got := api.ticketNotificationLocation(request); got != "中国江苏省南京市" || resolver.calls != 1 {
		t.Fatalf("IPv4 location = %q calls=%d", got, resolver.calls)
	}

	request.RemoteAddr = "[2001:db8::1]:443"
	request.Header.Del("X-Forwarded-For")
	if got := api.ticketNotificationLocation(request); got != "NULL" || resolver.calls != 1 {
		t.Fatalf("IPv6 location = %q calls=%d", got, resolver.calls)
	}
	request.RemoteAddr = "[::ffff:114.114.114.114]:443"
	if got := api.ticketNotificationLocation(request); got != "NULL" || resolver.calls != 1 {
		t.Fatalf("IPv4-mapped IPv6 location = %q calls=%d", got, resolver.calls)
	}

	resolver.err = errors.New("lookup failed")
	request.RemoteAddr = "114.114.114.114:443"
	if got := api.ticketNotificationLocation(request); got != "未知" {
		t.Fatalf("failed lookup location = %q, want 未知", got)
	}
}

func TestTicketHTTPNotificationCarriesResolvedLocationToTelegramOutbox(t *testing.T) {
	resolver := &fixedTicketRegionResolver{wantIP: "192.0.2.1", region: "美国【Level3】"}
	api, database := newTestAPIWithTicketRegionResolver(t, resolver)
	createHTTPTestUser(t, database, "ticket-location@example.test", "ticket-location-password-123")
	recipient, err := database.CreateAdminUser(t.Context(), store.CreateAdminUserInput{
		Email: "ticket-location-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	telegramID := int64(9911)
	if _, _, err := database.UpdateAdminUser(t.Context(), recipient.ID, store.UpdateAdminUserInput{
		Revision: recipient.Revision, Email: recipient.Email, GroupID: recipient.GroupID,
		TransferEnable: recipient.TransferEnable, ExpiredAt: recipient.ExpiredAt,
		SpeedLimit: recipient.SpeedLimit, DeviceLimit: recipient.DeviceLimit, Banned: recipient.Banned,
		TelegramIDSet: true, TelegramID: &telegramID,
	}, fixedNow()); err != nil {
		t.Fatal(err)
	}
	settings, err := database.GetTelegramSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateTelegramSettings(t.Context(), recipient.ID, settings.Revision, store.SaveTelegramSettingsInput{
		BotEnabled: true, ReplaceBotToken: true, BotTokenCipher: []byte(strings.Repeat("x", 33)),
	}, fixedNow()); err != nil {
		t.Fatal(err)
	}

	client := loginAs(t, api, "ticket-location@example.test", "ticket-location-password-123")
	created := client.request(t, api, http.MethodPost, "/api/v1/tickets", `{"subject":"Location parity","level":0,"message":"Initial"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body)
	}
	job, claimed, err := database.ClaimTelegramMessage(t.Context(), "location-http-claim", fixedNow(), 30*time.Second)
	if err != nil || !claimed || job.ChatID != telegramID || !strings.Contains(job.Text, "位置: 美国【Level3】") {
		t.Fatalf("claimed notification=(%#v,%t,%v)", job, claimed, err)
	}
	if resolver.calls != 1 {
		t.Fatalf("resolver calls=%d, want 1", resolver.calls)
	}
}
