package flow

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func newTestService(t *testing.T) (*Service, sqlmock.Sqlmock) {
	t.Helper()
	db, mock := newMockDB(t)
	mock.MatchExpectationsInOrder(false)
	eng := NewEngine(db, nil, nil)
	svc, err := NewService(db, eng, nil)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return svc, mock
}

func TestValidateDefinitionTriggers(t *testing.T) {
	cases := []struct {
		name    string
		def     *Definition
		wantErr bool
	}{
		{"nil", nil, false},
		{"no triggers", &Definition{}, false},
		{
			name: "valid cron 5-field",
			def: &Definition{Triggers: []TriggerDef{
				{ID: "t1", Type: TriggerTypeCron, Config: map[string]any{"expression": "*/1 * * * *"}},
			}},
		},
		{
			name: "valid cron with timezone",
			def: &Definition{Triggers: []TriggerDef{
				{ID: "t1", Type: TriggerTypeCron, Config: map[string]any{"expression": "0 0 * * *", "timezone": "Asia/Shanghai"}},
			}},
		},
		{
			name: "invalid cron expression",
			def: &Definition{Triggers: []TriggerDef{
				{ID: "t1", Type: TriggerTypeCron, Config: map[string]any{"expression": "not-cron"}},
			}},
			wantErr: true,
		},
		{
			name: "missing expression",
			def: &Definition{Triggers: []TriggerDef{
				{ID: "t1", Type: TriggerTypeCron, Config: map[string]any{}},
			}},
			wantErr: true,
		},
		{
			name: "invalid timezone",
			def: &Definition{Triggers: []TriggerDef{
				{ID: "t1", Type: TriggerTypeCron, Config: map[string]any{"expression": "* * * * *", "timezone": "Mars/Olympus"}},
			}},
			wantErr: true,
		},
		{
			name: "webhook ok at this stage",
			def: &Definition{Triggers: []TriggerDef{
				{ID: "t1", Type: TriggerTypeWebhook, Config: map[string]any{"path": "/hooks/foo"}},
			}},
		},
		{
			name: "unsupported type",
			def: &Definition{Triggers: []TriggerDef{
				{ID: "t1", Type: "telegram", Config: map[string]any{}},
			}},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDefinitionTriggers(tc.def)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestService_NextTriggerAt_NoCron(t *testing.T) {
	svc, mock := newTestService(t)
	defer svc.Stop()
	// 列出 flow 的触发器 → 空
	mock.ExpectQuery(".*flow_triggers.*").
		WillReturnRows(sqlmock.NewRows([]string{}))
	if got := svc.NextTriggerAt("flow1"); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}
