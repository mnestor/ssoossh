package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
)

// ApprovalPageClaimer binds the /approve/<id> approval page to the first
// browser that fetches it. Defined here, in the implementing package, per
// repo convention; middleware.ApprovalClaimMiddleware is the consumer.
type ApprovalPageClaimer interface {
	ClaimApprovalPage(ctx context.Context, requestID, presentedToken, userAgent string) (ClaimPageResult, error)
}

// ClaimPageOutcome is what a document GET of /approve/<id> should do,
// as decided by ClaimApprovalPage.
type ClaimPageOutcome int

const (
	// ClaimPageClaimed: this GET is the first ever; the page is now bound
	// to this client and ClaimPageResult.Token must be set as its claim
	// cookie.
	ClaimPageClaimed ClaimPageOutcome = iota

	// ClaimPageMatched: a returning client presented the cookie that
	// claimed the page. Serve it.
	ClaimPageMatched

	// ClaimPageUnknownRequest: no such request. Nothing to claim; the SPA
	// renders its own not-found state after the API lookup fails.
	ClaimPageUnknownRequest

	// ClaimPageRejected: the page is claimed and this client is not the
	// claimant. The link is spent.
	ClaimPageRejected

	// ClaimPageCookieBlocked: the page was claimed moments ago by what
	// looks like this same browser, but no cookie came back — the browser
	// is refusing cookies. Distinct from ClaimPageRejected because without
	// its own message the user is stuck in a lockout loop that presents as
	// a server bug (the OIDC return leg always arrives cookieless for
	// them).
	ClaimPageCookieBlocked
)

// ClaimPageResult carries ClaimApprovalPage's decision. Token is set only
// for ClaimPageClaimed.
type ClaimPageResult struct {
	Outcome ClaimPageOutcome
	Token   string
}

// claimCookieBlockedWindow is how soon after a claim a cookieless revisit
// by the same user agent is read as "this browser refuses cookies" rather
// than "a second client". It has to cover the first page load plus the OIDC
// round trip (credentials, possibly MFA), which is why it is minutes rather
// than seconds. A scanner burn followed by the victim's click usually
// mismatches on user agent, so a generous window costs little.
const claimCookieBlockedWindow = 10 * time.Minute

// claimUserAgentMaxLen bounds what an attacker-controlled header can make
// the claim record store.
const claimUserAgentMaxLen = 400

// ClaimApprovalPage records or checks the browser-level claim on
// requestID's approval page, for a document GET of /approve/<id>.
//
// The first GET wins the claim: a token is minted, its hash stored on the
// row, and the caller is told to hand the token to the client as a cookie.
// Every later GET must present that cookie back. This deliberately makes a
// GET state-changing — see middleware.ApprovalClaimMiddleware for why.
//
// This is the browser-level half of request binding; bindRequester is the
// identity-level half made on the first authenticated touch. The two are
// independent controls: this one spends the URL before authentication ever
// happens, which is what lets a link scanner burn a phishing link.
func (s *CertRequestService) ClaimApprovalPage(ctx context.Context, requestID, presentedToken, userAgent string) (ClaimPageResult, error) {
	var req model.CertificateRequest
	if err := s.db.WithContext(ctx).First(&req, "id = ?", requestID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ClaimPageResult{Outcome: ClaimPageUnknownRequest}, nil
		}
		return ClaimPageResult{}, fmt.Errorf("failed to look up certificate request for claiming: %w", err)
	}

	return s.claimApprovalPage(ctx, &req, presentedToken, userAgent)
}

// claimApprovalPage is ClaimApprovalPage past the lookup, split out so a
// test can hand it a req that still thinks the row is unclaimed — the state
// a racing loser is in when the guarded UPDATE below affects zero rows
// (the in-memory sqlite test harness serializes connections, so the race
// itself cannot be staged; see bindRequester's tests for the same split).
func (s *CertRequestService) claimApprovalPage(ctx context.Context, req *model.CertificateRequest, presentedToken, userAgent string) (ClaimPageResult, error) {
	if len(userAgent) > claimUserAgentMaxLen {
		userAgent = userAgent[:claimUserAgentMaxLen]
	}

	if req.ClaimTokenHash == nil {
		token, hash, err := newClaimToken()
		if err != nil {
			// not covered: crypto/rand failure (see .claude/rules/test-go.md).
			return ClaimPageResult{}, err
		}

		// Guarded so two first GETs racing on an unclaimed page can't both
		// win: the loser sees RowsAffected == 0 and falls through to be
		// judged against the winner's claim below, rather than overwriting
		// it.
		result := s.db.WithContext(ctx).Model(&model.CertificateRequest{}).
			Where("id = ? AND claim_token_hash IS NULL", req.ID).
			Updates(map[string]any{
				"claim_token_hash": hash,
				"claimed_at":       time.Now().UTC(),
				"claim_user_agent": userAgent,
			})
		if result.Error != nil {
			// not covered: failing this query and not the lookup above
			// needs per-query DB fault injection, which this codebase has
			// no helper for.
			return ClaimPageResult{}, fmt.Errorf("failed to claim certificate request: %w", result.Error)
		}
		if result.RowsAffected == 1 {
			// The link was opened: the first time anything says so. No
			// actor, since the claim happens before authentication; the
			// user agent is what a reviewer reads to tell a person's
			// browser from a mail scanner that burned the link.
			s.auditRecord(ctx, AuditEvent{
				Action:     AuditCertClaimed,
				OccurredAt: time.Now(),
				Detail: withDetail(hostContextDetail(*req), map[string]any{
					"request_id": req.ID,
					"cert_type":  string(req.Type),
					"source_ip":  req.SourceIP,
					"user_agent": userAgent,
				}),
			})
			return ClaimPageResult{Outcome: ClaimPageClaimed, Token: token}, nil
		}

		if err := s.db.WithContext(ctx).First(req, "id = ?", req.ID).Error; err != nil {
			// not covered: failing the re-read and not the guarded UPDATE
			// above it needs per-query DB fault injection, which this
			// codebase has no helper for.
			return ClaimPageResult{}, fmt.Errorf("failed to re-read certificate request after a racing claim: %w", err)
		}
		if req.ClaimTokenHash == nil {
			// not covered: unreachable defensive branch — the guarded
			// update only loses to a writer that set the hash, and nothing
			// ever clears it.
			return ClaimPageResult{}, errors.New("certificate request unclaimed after losing the claim race")
		}
	}

	if presentedToken != "" &&
		subtle.ConstantTimeCompare([]byte(hashClaimToken(presentedToken)), []byte(*req.ClaimTokenHash)) == 1 {
		return ClaimPageResult{Outcome: ClaimPageMatched}, nil
	}

	// A cookieless revisit by the same (non-empty) user agent, soon after
	// the claim, is the claiming browser itself refusing cookies — the
	// first GET and the OIDC return leg are the same navigation from the
	// user's point of view.
	if presentedToken == "" && userAgent != "" && userAgent == req.ClaimUserAgent &&
		req.ClaimedAt != nil && time.Since(*req.ClaimedAt) <= claimCookieBlockedWindow {
		return ClaimPageResult{Outcome: ClaimPageCookieBlocked}, nil
	}

	// A claimed page hit by a different client essentially never happens in
	// the legitimate flow, which makes this a high-signal phishing
	// indicator: most often a mail/chat scanner burned the link and this is
	// the person it was sent to. Log both sides for detection.
	var claimedAt time.Time
	if req.ClaimedAt != nil {
		claimedAt = *req.ClaimedAt
	}
	slog.Warn("approval page reopened by a client that did not claim it; the link is spent",
		slog.String("request_id", req.ID),
		slog.Time("claimed_at", claimedAt),
		slog.String("claiming_user_agent", req.ClaimUserAgent),
		slog.String("rejected_user_agent", userAgent),
		slog.Bool("cookie_presented", presentedToken != ""),
	)
	return ClaimPageResult{Outcome: ClaimPageRejected}, nil
}

// newClaimToken mints a claim cookie value and the hash stored for it: 256
// bits from crypto/rand, so the cookie is as unguessable as the request ID
// it protects.
func newClaimToken() (token, hash string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		// not covered: crypto/rand failure (see .claude/rules/test-go.md).
		return "", "", fmt.Errorf("failed to generate a claim token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, hashClaimToken(token), nil
}

// hashClaimToken is the one definition of how a claim cookie value maps to
// its stored form, so minting and checking cannot drift apart.
func hashClaimToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
