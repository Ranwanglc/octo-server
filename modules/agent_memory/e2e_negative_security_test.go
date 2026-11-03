package agent_memory

import (
	"errors"
	"testing"
	"time"

	"github.com/gocraft/dbr/v2"
)

func TestE2ENegativeSecurityRevocationAndPolicyOutageFailClosed(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*agentContextTestStore)
		want   error
	}{
		{
			name: "profile revoked",
			mutate: func(store *agentContextTestStore) {
				store.profile.Status = "revoked"
			},
			want: ErrAgentContextAuthentication,
		},
		{
			name: "grant revoked",
			mutate: func(store *agentContextTestStore) {
				now := time.Date(2026, 7, 28, 10, 49, 0, 0, time.UTC)
				store.grants[0].RevokedAt = &now
			},
			want: ErrAgentContextForbidden,
		},
		{
			name: "policy outage",
			mutate: func(store *agentContextTestStore) {
				store.policyErr = errors.New("synthetic policy secret must not escape")
			},
			want: ErrAgentContextAuthentication,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, mock, cleanup := newAgentContextTestService(t, nil)
			defer cleanup()
			store := validAgentContextStore()
			service.store = store
			expectSuccessfulRobotResolution(mock, "agent-token")
			issued, err := service.Resolve(agentContextRequest{BearerToken: "agent-token", Operation: CapabilityRecall})
			if err != nil {
				t.Fatal(err)
			}
			if issued.ExpiresAt.Sub(issued.IssuedAt) != 5*time.Second {
				t.Fatalf("context lifetime = %s", issued.ExpiresAt.Sub(issued.IssuedAt))
			}

			test.mutate(store)
			expectPublishedRobot(mock, "agent-token")
			started := time.Now()
			context, err := service.Resolve(agentContextRequest{BearerToken: "agent-token", Operation: CapabilityRecall})
			if context != nil || !errors.Is(err, test.want) {
				t.Fatalf("Resolve() after %s = %+v, %v", test.name, context, err)
			}
			if elapsed := time.Since(started); elapsed >= 5*time.Second {
				t.Fatalf("revocation/policy denial took %s", elapsed)
			}
			if errors.Is(err, dbr.ErrNotFound) || err.Error() == "synthetic policy secret must not escape" {
				t.Fatalf("unsanitized internal error = %v", err)
			}
		})
	}
}
