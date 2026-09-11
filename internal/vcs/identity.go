package vcs

import (
	"context"
	"fmt"
)

// postingActor resolves the credential's posting identity independently of any
// comment body. Installation tokens need an operator-supplied bot login because
// GitHub's authenticated-user endpoint only accepts user credentials.
func (g *GitHub) postingActor(ctx context.Context) (int64, error) {
	g.identityMu.Lock()
	defer g.identityMu.Unlock()
	if g.actorID > 0 {
		return g.actorID, nil
	}
	user, _, err := g.client.Users.Get(ctx, g.actorLogin)
	if err != nil {
		return 0, fmt.Errorf("github: identify review author (set NITPICK_BOT_LOGIN for an installation token): %w", err)
	}
	if user.GetID() <= 0 {
		return 0, fmt.Errorf("github: review author has no account ID")
	}
	g.actorID = user.GetID()
	return g.actorID, nil
}
