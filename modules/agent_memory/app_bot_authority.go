package agent_memory

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gocraft/dbr/v2"
)

var ErrRobotGovernanceFactsUnavailable = errors.New("robot governance facts unavailable")

type robotAuthorityRecord struct {
	RobotID         string `db:"robot_id"`
	CreatorUID      string `db:"creator_uid"`
	SpaceID         string `db:"space_id"`
	Status          int    `db:"status"`
	CredentialEpoch int64  `db:"credential_epoch"`
}

type RobotAuthorityAdapter struct{ session *dbr.Session }

func NewRobotAuthorityAdapter(session *dbr.Session) *RobotAuthorityAdapter {
	return &RobotAuthorityAdapter{session: session}
}

func (adapter *RobotAuthorityAdapter) LoadAuthoritativeRobot(ctx context.Context, robotID string) (AuthoritativeRobotState, error) {
	if adapter == nil || adapter.session == nil || strings.TrimSpace(robotID) == "" {
		return AuthoritativeRobotState{}, ErrRobotGovernanceFactsUnavailable
	}
	var record robotAuthorityRecord
	err := adapter.session.Select("robot_id", "creator_uid", "space_id", "status", "credential_epoch").
		From("robot").Where("robot_id=?", robotID).LoadOneContext(ctx, &record)
	if err != nil {
		return AuthoritativeRobotState{}, fmt.Errorf("%w: %v", ErrRobotGovernanceFactsUnavailable, err)
	}
	state := AuthoritativeRobotState{
		RobotID: record.RobotID, AgentUID: record.RobotID, OwnerUID: record.CreatorUID,
		ScopeType: ProfileScopeSpace, CredentialEpoch: record.CredentialEpoch, RobotStatus: record.Status,
	}
	if strings.TrimSpace(record.SpaceID) != "" {
		spaceID := record.SpaceID
		state.SpaceID = &spaceID
	}
	if record.Status == 1 {
		state.Lifecycle = RobotLifecycleActive
	} else {
		state.Lifecycle = RobotLifecycleDisabled
	}
	if err := state.Validate(); err != nil {
		return AuthoritativeRobotState{}, err
	}
	return state, nil
}
