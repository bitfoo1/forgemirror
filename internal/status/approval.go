package status

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"time"
)

type approvalRequiredError struct{}

func (approvalRequiredError) Error() string      { return "first publication requires approval" }
func (approvalRequiredError) ReasonCode() string { return "approval_required" }

// ErrApprovalRequired distinguishes publication policy from transfer failure.
var ErrApprovalRequired = approvalRequiredError{}

// Approval authorizes ongoing publication to an identity, not one exact snapshot.
type Approval struct {
	Repo     string    `json:"repo"`
	Target   string    `json:"target"`
	Identity string    `json:"identity"`
	At       time.Time `json:"at"`
}

// PublicationID binds source and destination without persisting clone URLs.
func PublicationID(source, destination string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(source+"\x00"+destination)))
}

func (s *Store) approvedLocked(repo, target, identity string) bool {
	return slices.ContainsFunc(
		s.approvals,
		func(a Approval) bool { return a.Repo == repo && a.Target == target && a.Identity == identity },
	)
}

// Approved reports whether the current mapping matches a durable grant.
func (s *Store) Approved(repo, target, identity string) bool {
	s.controlMu.RLock()
	defer s.controlMu.RUnlock()
	return s.approvedLocked(repo, target, identity)
}

// BeginPublication checks pause and approval under the same operation barrier.
func (s *Store) BeginPublication(repo, target, identity string, required bool) (func(), error) {
	s.controlMu.RLock()
	if _, ok := s.pauseLocked(repo, target); ok {
		s.controlMu.RUnlock()
		return nil, ErrPaused
	}
	if required && !s.approvedLocked(repo, target, identity) {
		s.controlMu.RUnlock()
		return nil, ErrApprovalRequired
	}
	return s.controlMu.RUnlock, nil
}

// Approve replaces only this repository/target grant after durable persistence.
func (s *Store) Approve(ctx context.Context, repo, target, identity string) error {
	if repo == "" || target == "" || !validIdentity(identity) {
		return errors.New("invalid publication approval")
	}
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	previous := s.approvals
	next := slices.DeleteFunc(
		slices.Clone(previous),
		func(a Approval) bool { return a.Repo == repo && a.Target == target },
	)
	next = append(next, Approval{Repo: repo, Target: target, Identity: identity, At: s.now()})
	s.approvals = next
	if err := s.saveControls(ctx, s.pauses); err != nil {
		s.approvals = previous
		return err
	}
	return nil
}

func validIdentity(id string) bool {
	decoded, err := hex.DecodeString(id)
	return err == nil && len(decoded) == sha256.Size
}

func validApprovals(approvals []Approval) bool {
	for _, a := range approvals {
		if a.Repo == "" || a.Target == "" || !validIdentity(a.Identity) {
			return false
		}
	}
	return true
}
