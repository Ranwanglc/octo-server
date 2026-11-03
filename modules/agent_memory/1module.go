package agent_memory

import (
	"embed"
	"os"
	"strings"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/register"
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	appwkhttp "github.com/Mininglamp-OSS/octo-server/pkg/wkhttp"
	"github.com/gocraft/dbr/v2"
)

const memoryControlEnabledEnv = "MEMORY_CONTROL_ENABLED"
const memoryGatewayServiceSecretEnv = "MEMORY_GATEWAY_SERVICE_SECRET"
const memoryGateURL = "MEMORY_GATE_URL"
const memoryGateReadinessToken = "MEMORY_GATE_READINESS_TOKEN"

//go:embed sql
var sqlFS embed.FS

func init() {
	register.AddModule(func(ctx interface{}) register.Module {
		return newModule(ctx.(*config.Context), memoryControlEnabled(os.Getenv(memoryControlEnabledEnv)))
	})
}

func memoryControlEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true":
		return true
	default:
		return false
	}
}

func newModule(ctx *config.Context, enabled bool) register.Module {
	module := register.Module{Name: "agent_memory"}
	if !enabled {
		return module
	}
	return newEnabledModuleWithContext(ctx)
}

func newEnabledModule(session *dbr.Session) register.Module {
	return newEnabledModuleRuntime(session, nil)
}

func newEnabledModuleWithContext(ctx *config.Context) register.Module {
	return newEnabledModuleRuntime(ctx.DB(), ctx)
}

func newEnabledModuleRuntime(session *dbr.Session, ctx *config.Context) register.Module {
	store := NewStore(session)
	bankService := NewBankService(store)
	authority := NewRobotAuthorityAdapter(session)
	access := NewManagementAccessAdapter(session)
	enablement := NewEnablementService(store)
	enablement.probe = &HTTPReadinessProbe{URL: os.Getenv(memoryGateURL), ServiceToken: os.Getenv(memoryGateReadinessToken)}
	runtime := &Runtime{
		Profiles:             NewProfileService(authority, store),
		Catalog:              store,
		Access:               access,
		SpaceFacts:           access,
		Banks:                bankService,
		BankCreator:          bankService,
		Grants:               NewGrantService(store, store),
		Collaboration:        NewCollaborationService(store),
		Enablement:           enablement,
		Audits:               NewAuditService(store),
		Contexts:             NewAgentContextService(session, store),
		Persistence:          NewPersistenceService(store, NewAgentContextService(session, store)),
		Governance:           NewGovernanceService(store, NewActivePolicyResolver(store)),
		Lifecycle:            NewLifecycleService(store, NewAgentContextService(session, store)),
		gatewayServiceSecret: os.Getenv(memoryGatewayServiceSecretEnv),
		ctx:                  ctx,
	}

	return register.Module{
		Name:     "agent_memory",
		SetupAPI: func() register.APIRouter { return runtime },
		SQLDir:   register.NewSQLFS(sqlFS),
		Service:  runtime,
	}
}

type Runtime struct {
	Profiles             *ProfileService
	Catalog              ProfileCatalog
	Access               ManagementAccess
	SpaceFacts           SpaceOwnershipFacts
	Banks                BankManagementStore
	BankCreator          LogicalBankCreator
	OrganizationFacts    OrganizationFacts
	Grants               *GrantService
	Collaboration        *CollaborationService
	Enablement           *EnablementService
	Audits               *AuditService
	Contexts             *AgentContextService
	Persistence          *PersistenceService
	Governance           *GovernanceService
	Lifecycle            *LifecycleService
	ctx                  *config.Context
	gatewayServiceSecret string
}

func (runtime *Runtime) Route(r *wkhttp.WKHttp) {
	if runtime == nil || runtime.ctx == nil || runtime.Profiles == nil || runtime.Catalog == nil || runtime.Access == nil || runtime.Grants == nil || runtime.Audits == nil || runtime.Collaboration == nil || runtime.Enablement == nil {
		return
	}
	memory := r.Group(
		"/v1/memory",
		runtime.ctx.AuthMiddleware(r),
		appwkhttp.SharedUIDRateLimiter(r, runtime.ctx),
	)
	memory.GET("/agents", runtime.listProfiles)
	memory.GET("/agents/:agent_uid", runtime.getProfile)
	memory.PUT("/agents/:agent_uid", runtime.updateProfile)
	memory.GET("/banks", runtime.listBanks)
	memory.POST("/banks", runtime.createBank)
	memory.RouterGroup.PATCH("/banks/:bank_id", r.WKHttpHandler(runtime.updateBank))
	memory.POST("/agents/:agent_uid/banks", runtime.createAgentBank)
	memory.POST("/spaces/:space_id/banks", runtime.createSpaceBank)
	memory.GET("/banks/:bank_id/offers", runtime.listBankOffers)
	memory.POST("/banks/:bank_id/offers", runtime.createBankOffer)
	memory.RouterGroup.PATCH("/offers/:offer_id", r.WKHttpHandler(runtime.reviseOffer))
	memory.GET("/agents/:agent_uid/offers", runtime.listRobotOffers)
	memory.PUT("/offers/:offer_id/decision", runtime.decideOffer)
	memory.POST("/acceptances/:acceptance_id/withdraw", runtime.withdrawAcceptance)
	memory.GET("/agents/:agent_uid/effective-permissions", runtime.effectivePermissions)
	memory.POST("/agents/:agent_uid/enablement-attempts", runtime.startEnablement)
	memory.GET("/enablement-attempts/:attempt_id", runtime.getEnablement)
	memory.POST("/enablement-attempts/:attempt_id/probe", runtime.probeEnablement)
	memory.POST("/enablement-attempts/:attempt_id/commit", runtime.commitEnablement)
	memory.POST("/enablement-attempts/:attempt_id/cancel", runtime.cancelEnablement)
	memory.GET("/governance/runs", runtime.listGovernanceRuns)
	memory.POST("/governance/runs", runtime.createGovernanceRun)
	memory.GET("/governance/runs/:run_id", runtime.getGovernanceRun)
	memory.POST("/governance/runs/:run_id/rollback", runtime.rollbackGovernanceRun)

	// This internal route uses two dedicated credentials and intentionally skips user auth.
	r.POST("/v1/internal/memory/agent-context", runtime.agentContext)
	r.POST("/v1/internal/memory/readiness-context", runtime.readinessContext)
	r.POST("/v1/internal/memory/persistence/prepare", runtime.preparePersistence)
	r.POST("/v1/internal/memory/persistence/complete", runtime.completePersistence)
	r.POST("/v1/internal/memory/persistence/fail", runtime.failPersistence)
	r.POST("/v1/internal/memory/persistence/status", runtime.persistenceStatus)
	r.POST("/v1/internal/memory/lifecycle/transition", runtime.applyLifecycleTransition)
	r.POST("/v1/internal/memory/lifecycle/rollback", runtime.rollbackLifecycle)
}
