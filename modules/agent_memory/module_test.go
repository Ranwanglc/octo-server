package agent_memory

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/gocraft/dbr/v2"
)

func TestMemoryControlEnabledDefaultsFalse(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{value: "", want: false},
		{value: "false", want: false},
		{value: "unexpected", want: false},
		{value: "true", want: true},
		{value: " TRUE ", want: true},
		{value: "1", want: true},
	}
	for _, test := range tests {
		if got := memoryControlEnabled(test.value); got != test.want {
			t.Fatalf("memoryControlEnabled(%q) = %t, want %t", test.value, got, test.want)
		}
	}
}

func TestDisabledModuleRegistersNoRuntimeBehavior(t *testing.T) {
	module := newModule(nil, false)
	if module.Name != "agent_memory" {
		t.Fatalf("module name = %q, want agent_memory", module.Name)
	}
	if module.SQLDir != nil || module.SetupAPI != nil || module.Service != nil || module.Start != nil || module.Stop != nil {
		t.Fatalf("disabled module registered behavior: %+v", module)
	}
}

func TestEnabledModuleRegistrationIsDeterministicAndRouteEmpty(t *testing.T) {
	session, cleanup := newModuleTestSession(t)
	defer cleanup()

	first := newEnabledModule(session)
	second := newEnabledModule(session)
	if first.Name != "agent_memory" || second.Name != first.Name {
		t.Fatalf("module names = %q and %q", first.Name, second.Name)
	}
	if first.SQLDir == nil || second.SQLDir == nil || first.SetupAPI == nil || second.SetupAPI == nil {
		t.Fatal("enabled module must register migrations and router lifecycle")
	}
	if first.Start != nil || first.Stop != nil || second.Start != nil || second.Stop != nil {
		t.Fatal("T010 must not register background behavior")
	}

	for _, registered := range []interface{}{first.Service, second.Service} {
		runtime, ok := registered.(*Runtime)
		if !ok || runtime.Profiles == nil || runtime.Banks == nil || runtime.BankCreator == nil {
			t.Fatalf("enabled service = %T, want initialized *Runtime", registered)
		}
	}

	router := first.SetupAPI()
	if router != first.Service {
		t.Fatal("SetupAPI and Service must expose the same runtime")
	}
	httpRouter := wkhttp.New()
	router.Route(httpRouter)
	request := httptest.NewRequest(http.MethodGet, "/v1/agent_memory", nil)
	response := httptest.NewRecorder()
	httpRouter.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("empty T010 router status = %d, want 404", response.Code)
	}

	entries, err := first.SQLDir.ReadDir("sql")
	if err != nil {
		t.Fatal(err)
	}
	wantMigrations := []string{
		"20260723000001_agent_memory_control_plane.sql",
		"20260730000002_robot_profile_authority.sql",
		"20260730000003_memory_collaboration.sql",
	}
	if len(entries) != len(wantMigrations) {
		t.Fatalf("registered migrations = %v", entries)
	}
	for index, name := range wantMigrations {
		if entries[index].Name() != name {
			t.Fatalf("registered migrations = %v", entries)
		}
	}
}

func TestEnabledRuntimeUsesRobotAuthority(t *testing.T) {
	session, cleanup := newModuleTestSession(t)
	defer cleanup()

	runtime := newEnabledModule(session).Service.(*Runtime)
	authority, ok := runtime.Profiles.authority.(*RobotAuthorityAdapter)
	if !ok || authority.session == nil {
		t.Fatal("enabled runtime must use authoritative Robot synchronization")
	}
}

func newModuleTestSession(t *testing.T) (*dbr.Session, func()) {
	t.Helper()
	rawDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	connection := &dbr.Connection{DB: rawDB, Dialect: nil}
	return connection.NewSession(nil), func() { _ = rawDB.Close() }
}

func TestAgentMemoryAPINoLegacyResponseError(t *testing.T) {
	source, err := os.ReadFile("api.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"c.ResponseError(",
		"c.ResponseErrorf(",
		"c.AbortWithStatusJSON(",
		"c.JSON(http.StatusBadRequest",
		"c.JSON(http.StatusForbidden",
		"c.JSON(http.StatusNotFound",
		"c.JSON(http.StatusConflict",
		"c.JSON(http.StatusInternalServerError",
	} {
		if strings.Contains(string(source), forbidden) {
			t.Fatalf("api.go contains forbidden direct error response %q", forbidden)
		}
	}
}
