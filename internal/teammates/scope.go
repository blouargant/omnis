package teammates

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"
)

// ownerResolver maps a mailbox address to the login owning its session. It is
// installed by a shared multi-user server (cookie identity mode) so teammate
// messaging never crosses users; nil ⇒ no scoping (single-user behaviour).
var ownerResolver atomic.Pointer[func(addr string) string]

// SetOwnerResolver installs (or, with nil, removes) the process-wide
// address→owner resolver. Unknown addresses must resolve to "".
func SetOwnerResolver(f func(addr string) string) {
	if f == nil {
		ownerResolver.Store(nil)
		return
	}
	ownerResolver.Store(&f)
}

// AddressOwner resolves addr's owner. enabled is false when no resolver is
// installed (no scoping applies).
func AddressOwner(addr string) (owner string, enabled bool) {
	p := ownerResolver.Load()
	if p == nil {
		return "", false
	}
	return (*p)(addr), true
}

// errNoSuchPeer is deliberately indistinguishable from "no such session": a
// caller must not learn whether another user's session name exists.
func errNoSuchPeer(to string) error {
	return fmt.Errorf("unknown recipient %q (cross-session messages can only reach your own sessions)", to)
}

// scopeCheck refuses a message from fromAddr to toAddr when scoping is on and
// the two do not share a known owner.
func scopeCheck(fromAddr, toAddr string) error {
	from, on := AddressOwner(fromAddr)
	if !on {
		return nil
	}
	if to, _ := AddressOwner(toAddr); from == "" || to != from {
		return errNoSuchPeer(toAddr)
	}
	return nil
}

// visibleSessions is the teammate_list view for the caller at myAddr: every
// registered session without scoping, else only those sharing its owner.
func (a *Agent) visibleSessions(myAddr string) map[string]string {
	all := a.Registry.List()
	me, on := AddressOwner(myAddr)
	if !on {
		return all
	}
	out := make(map[string]string, len(all))
	if me == "" {
		return out
	}
	for name, addr := range all {
		if o, _ := AddressOwner(addr); o == me {
			out[name] = addr
		}
	}
	return out
}

// tellAs / askAs are tellWith / askWith behind the owner scope check.
func (a *Agent) tellAs(ctx context.Context, from, to, body string) error {
	if err := scopeCheck(from, to); err != nil {
		return err
	}
	return a.tellWith(ctx, from, to, body)
}

func (a *Agent) askAs(ctx context.Context, from, to, question string, timeout time.Duration) (string, error) {
	if err := scopeCheck(from, to); err != nil {
		return "", err
	}
	return a.askWith(ctx, from, to, question, timeout)
}
