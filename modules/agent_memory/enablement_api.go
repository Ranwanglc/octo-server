package agent_memory

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/pkg/errcode"
	"github.com/Mininglamp-OSS/octo-server/pkg/httperr"
)

type enablementRequest struct {
	IdempotencyKey  string `json:"idempotency_key"`
	ExpectedVersion *int64 `json:"expected_version"`
}

func (runtime *Runtime) startEnablement(c *wkhttp.Context) {
	record, _, ok := runtime.authorizedProfile(c)
	if !ok {
		return
	}
	caller := managementCaller(c)
	if caller.UID != record.OwnerUID && !isPlatformAdmin(caller) {
		respondForbidden(c)
		return
	}
	var request enablementRequest
	if decodeStrictJSON(c, &request) != nil {
		respondInvalid(c)
		return
	}
	attempt, err := runtime.Enablement.Start(record.RobotID, caller.UID, request.IdempotencyKey)
	runtime.finishEnablement(c, attempt, err, http.StatusCreated)
}

func (runtime *Runtime) getEnablement(c *wkhttp.Context) {
	attempt, err := runtime.Enablement.store.FindEnablementAttempt(strings.TrimSpace(c.Param("attempt_id")))
	if err != nil {
		runtime.finishEnablement(c, nil, err, http.StatusOK)
		return
	}
	if !runtime.authorizeEnablement(c, attempt) {
		return
	}
	c.Response(attempt.View(time.Now().UTC()))
}

func (runtime *Runtime) probeEnablement(c *wkhttp.Context)  { runtime.mutateEnablement(c, "probe") }
func (runtime *Runtime) commitEnablement(c *wkhttp.Context) { runtime.mutateEnablement(c, "commit") }
func (runtime *Runtime) cancelEnablement(c *wkhttp.Context) { runtime.mutateEnablement(c, "cancel") }

func (runtime *Runtime) mutateEnablement(c *wkhttp.Context, operation string) {
	attempt, err := runtime.Enablement.store.FindEnablementAttempt(strings.TrimSpace(c.Param("attempt_id")))
	if err != nil {
		runtime.finishEnablement(c, nil, err, http.StatusOK)
		return
	}
	if !runtime.authorizeEnablement(c, attempt) {
		return
	}
	var request enablementRequest
	if decodeStrictJSON(c, &request) != nil || request.ExpectedVersion == nil {
		respondInvalid(c)
		return
	}
	var updated *EnablementAttempt
	switch operation {
	case "probe":
		updated, err = runtime.Enablement.Probe(attempt.ID, managementCaller(c).UID, *request.ExpectedVersion)
	case "commit":
		updated, err = runtime.Enablement.Commit(attempt.ID, managementCaller(c).UID, *request.ExpectedVersion)
	case "cancel":
		updated, err = runtime.Enablement.Cancel(attempt.ID, managementCaller(c).UID, *request.ExpectedVersion)
	}
	runtime.finishEnablement(c, updated, err, http.StatusOK)
}

func (runtime *Runtime) authorizeEnablement(c *wkhttp.Context, attempt *EnablementAttempt) bool {
	facts, err := runtime.Collaboration.store.FindRobotCollaborationFacts(attempt.RobotID)
	if err != nil {
		runtime.respondInternal(c, "authorize enablement", err)
		return false
	}
	caller := managementCaller(c)
	if caller.UID != facts.OwnerUID && !isPlatformAdmin(caller) {
		respondNotFound(c)
		return false
	}
	return true
}

func (runtime *Runtime) finishEnablement(c *wkhttp.Context, attempt *EnablementAttempt, err error, status int) {
	switch {
	case err == nil:
		c.ResponseWithStatus(status, attempt.View(time.Now().UTC()))
	case errors.Is(err, ErrEnablementNotFound):
		respondNotFound(c)
	case errors.Is(err, ErrVersionConflict), errors.Is(err, ErrEnablementBlocked):
		httperr.ResponseErrorLWithStatus(c, errcode.ErrAgentMemoryVersionConflict, nil, nil)
	default:
		runtime.respondInternal(c, "enablement operation", err)
	}
}
