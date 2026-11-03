package agent_memory

import (
	"context"
	"errors"

	"github.com/gocraft/dbr/v2"
)

var ErrManagementFactsUnavailable = errors.New("agent memory management facts unavailable")

type ManagementCaller struct {
	UID          string
	PlatformRole string
}

type ManagementDecision struct {
	Visible   bool
	CanManage bool
}

type ManagementAccess interface {
	AuthorizeProfile(context.Context, ManagementCaller, AgentMemoryProfile) (ManagementDecision, error)
	AuthorizeSpace(context.Context, ManagementCaller, string) (ManagementDecision, error)
}

type ManagementAccessAdapter struct {
	session *dbr.Session
}

func NewManagementAccessAdapter(session *dbr.Session) *ManagementAccessAdapter {
	return &ManagementAccessAdapter{session: session}
}

func (adapter *ManagementAccessAdapter) AuthorizeProfile(ctx context.Context, caller ManagementCaller, profile AgentMemoryProfile) (ManagementDecision, error) {
	if caller.UID != "" && profile.OwnerUID == caller.UID {
		return ManagementDecision{Visible: true, CanManage: true}, nil
	}
	switch profile.ScopeType {
	case ProfileScopePlatform:
		canManage := caller.PlatformRole == "admin" || caller.PlatformRole == "superAdmin"
		return ManagementDecision{Visible: canManage, CanManage: canManage}, nil
	case ProfileScopeSpace:
		if profile.SpaceID == nil {
			return ManagementDecision{}, nil
		}
		return adapter.AuthorizeSpace(ctx, caller, *profile.SpaceID)
	default:
		return ManagementDecision{}, nil
	}
}

func (adapter *ManagementAccessAdapter) AuthorizeSpace(ctx context.Context, caller ManagementCaller, spaceID string) (ManagementDecision, error) {
	if adapter == nil || adapter.session == nil || caller.UID == "" || spaceID == "" {
		return ManagementDecision{}, ErrManagementFactsUnavailable
	}
	var member struct {
		Role int `db:"role"`
	}
	count, err := adapter.session.SelectBySql(
		"SELECT role FROM space_member WHERE space_id=? AND uid=? AND status=1 LIMIT 1",
		spaceID, caller.UID,
	).LoadContext(ctx, &member)
	if err != nil {
		return ManagementDecision{}, err
	}
	if count == 0 {
		return ManagementDecision{}, nil
	}
	return ManagementDecision{Visible: true, CanManage: member.Role >= 1}, nil
}

func (adapter *ManagementAccessAdapter) SpaceOwnerUID(ctx context.Context, spaceID string) (string, error) {
	if adapter == nil || adapter.session == nil || spaceID == "" {
		return "", ErrManagementFactsUnavailable
	}
	var space struct {
		Creator string `db:"creator"`
	}
	count, err := adapter.session.SelectBySql(
		"SELECT creator FROM space WHERE space_id=? AND status=1 LIMIT 1",
		spaceID,
	).LoadContext(ctx, &space)
	if err != nil {
		return "", err
	}
	if count == 0 || space.Creator == "" {
		return "", ErrManagementFactsUnavailable
	}
	return space.Creator, nil
}
