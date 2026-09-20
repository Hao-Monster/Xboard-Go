package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestSCH002ActivationScheduleCrossMidnightAPIAndInvalidSaveAreAtomic(t *testing.T) {
	location, err := time.LoadLocation("Asia/Singapore")
	if err != nil {
		t.Fatalf("LoadLocation() error = %v", err)
	}

	cases := []struct {
		name               string
		now                time.Time
		wantNextTransition time.Time
	}{
		{
			name:               "late in active window rolls to next day",
			now:                time.Date(2026, 8, 20, 23, 30, 0, 0, location),
			wantNextTransition: time.Date(2026, 8, 21, 2, 0, 0, 0, location),
		},
		{
			name:               "early in active window rolls to same day",
			now:                time.Date(2026, 8, 21, 1, 30, 0, 0, location),
			wantNextTransition: time.Date(2026, 8, 21, 2, 0, 0, 0, location),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			caseTime := tc.now
			api, database := newTestAPIWithAllOptionsAndModifier(t, nil, true, nil, nil, false, nil, nil, func(d *Dependencies) {
				d.Now = func() time.Time { return caseTime }
			})
			admin := loginAdmin(t, api)
			ctx := t.Context()

			machine, _, err := database.CreateMachine(ctx, store.CreateMachineInput{Name: "sch002-edge", IsActive: true}, caseTime)
			if err != nil {
				t.Fatalf("CreateMachine() error = %v", err)
			}
			node, err := database.CreateNode(ctx, store.CreateNodeInput{
				Name: "sch002-node", Type: "vless", Host: "sch002.example.test", Port: "443", Show: true, Enabled: false, MachineID: &machine.ID,
			}, caseTime)
			if err != nil {
				t.Fatalf("CreateNode() error = %v", err)
			}

			path := fmt.Sprintf("/api/v1/admin/admin/nodes/%d/activation-schedule", node.ID)
			saved := admin.request(t, api, http.MethodPut, path, `{"schedule_type":"daily","timezone":"Asia/Singapore","enable_time":"23:00","disable_time":"02:00"}`)
			if saved.Code != http.StatusOK {
				t.Fatalf("save schedule status = %d; body=%s", saved.Code, saved.Body)
			}
			var payload struct {
				Data struct {
					Phase             string    `json:"phase"`
					NextTransitionAt  time.Time `json:"next_transition_at"`
					NextTargetEnabled bool      `json:"next_target_enabled"`
					Revision          string    `json:"revision"`
				} `json:"data"`
			}
			decodeResponse(t, saved, &payload)
			if strings.TrimSpace(payload.Data.Revision) == "" {
				t.Fatal("save schedule response returned an empty revision")
			}
			if payload.Data.Phase != "active" || !payload.Data.NextTransitionAt.Equal(tc.wantNextTransition) || payload.Data.NextTargetEnabled {
				t.Fatalf("unexpected schedule response: %#v", payload.Data)
			}

			actualNode, err := database.GetNode(ctx, node.ID)
			if err != nil {
				t.Fatalf("GetNode() after save error = %v", err)
			}
			if !actualNode.Enabled {
				t.Fatal("schedule did not immediately enable linked node")
			}
			nodeBeforeInvalid := actualNode
			scheduleBeforeInvalid, err := database.GetActivationSchedule(ctx, node.ID)
			if err != nil {
				t.Fatalf("GetActivationSchedule() after save error = %v", err)
			}

			got := admin.request(t, api, http.MethodGet, path, "")
			if got.Code != http.StatusOK {
				t.Fatalf("get schedule status = %d; body=%s", got.Code, got.Body)
			}
			var persisted struct {
				Data store.ActivationSchedule `json:"data"`
			}
			decodeResponse(t, got, &persisted)
			if persisted.Data.EnableTime != "23:00" || persisted.Data.DisableTime != "02:00" || persisted.Data.Timezone != "Asia/Singapore" || persisted.Data.Revision != payload.Data.Revision || persisted.Data.Revision != scheduleBeforeInvalid.Revision || !persisted.Data.NextTransitionAt.Equal(tc.wantNextTransition) {
				t.Fatalf("persisted schedule = %#v", persisted.Data)
			}

			invalid := admin.request(t, api, http.MethodPut, path, `{"schedule_type":"daily","timezone":"Asia/Singapore","enable_time":"02:00","disable_time":"02:00"}`)
			if invalid.Code != http.StatusUnprocessableEntity {
				t.Fatalf("equal boundary status = %d, want %d; body=%s", invalid.Code, http.StatusUnprocessableEntity, invalid.Body)
			}

			nodeAfterInvalid, err := database.GetNode(ctx, node.ID)
			if err != nil {
				t.Fatalf("GetNode() after invalid save error = %v", err)
			}
			scheduleAfterInvalid, err := database.GetActivationSchedule(ctx, node.ID)
			if err != nil {
				t.Fatalf("GetActivationSchedule() after invalid save error = %v", err)
			}
			if nodeAfterInvalid.Enabled != nodeBeforeInvalid.Enabled || nodeAfterInvalid.Revision != nodeBeforeInvalid.Revision || scheduleAfterInvalid != scheduleBeforeInvalid {
				t.Fatalf("invalid save mutated state: node before=%#v after=%#v schedule before=%#v after=%#v", nodeBeforeInvalid, nodeAfterInvalid, scheduleBeforeInvalid, scheduleAfterInvalid)
			}
		})
	}
}
