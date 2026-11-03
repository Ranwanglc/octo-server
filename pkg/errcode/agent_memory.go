package errcode

import (
	"net/http"

	"github.com/Mininglamp-OSS/octo-server/pkg/i18n/codes"
)

var ErrAgentMemoryVersionConflict = register(codes.Code{
	ID:             "err.server.agent_memory.version_conflict",
	HTTPStatus:     http.StatusConflict,
	DefaultMessage: "The Agent memory resource was modified by another request.",
})

var ErrAgentMemoryContextAuthFailed = register(codes.Code{
	ID:             "err.server.agent_memory.context_auth_failed",
	HTTPStatus:     http.StatusUnauthorized,
	DefaultMessage: "Agent memory authentication failed.",
})

var ErrAgentMemoryContextForbidden = register(codes.Code{
	ID:             "err.server.agent_memory.context_forbidden",
	HTTPStatus:     http.StatusForbidden,
	DefaultMessage: "Agent memory access is forbidden.",
})

var ErrAgentMemoryGovernanceScope = register(codes.Code{
	ID:             "err.server.agent_memory.governance_scope",
	HTTPStatus:     http.StatusBadRequest,
	DefaultMessage: "The governance scope or policy is not available for this run.",
})
