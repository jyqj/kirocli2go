package device

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/rand"
	"path/filepath"
	"strings"
)

// Identity represents a virtual device fingerprint bound to an account.
type Identity struct {
	Username string // e.g. "alexsmith"
	HomeDir  string // e.g. "/Users/alexsmith"
}

// ProjectDir generates a deterministic virtual project directory for a session
// under this identity's home directory.
// The result looks like: /Users/alexsmith/projects/webapp-backend
func (id Identity) ProjectDir(sessionKey string) string {
	h := sha256.Sum256([]byte(id.Username + ":" + sessionKey))
	idx := int(binary.LittleEndian.Uint32(h[:4]))
	name := projectNames[abs(idx)%len(projectNames)]
	return filepath.Join(id.HomeDir, "projects", name)
}

// ForAccount returns a deterministic virtual identity for the given account ID.
// Same account ID always produces the same identity.
func ForAccount(accountID string) Identity {
	h := sha256.Sum256([]byte("device-identity:" + accountID))
	idx := int(binary.LittleEndian.Uint32(h[:4]))
	username := usernames[abs(idx)%len(usernames)]
	return Identity{
		Username: username,
		HomeDir:  "/Users/" + username,
	}
}

// RandomIdentity returns a random identity (for accounts without stable IDs).
func RandomIdentity() Identity {
	username := usernames[rand.Intn(len(usernames))]
	return Identity{
		Username: username,
		HomeDir:  "/Users/" + username,
	}
}

// EffectiveWorkdir returns the virtual working directory to use in upstream requests.
// If the incoming workdir is empty or ".", returns identity's project dir for the session.
// Otherwise preserves the basename of the real workdir under the virtual home.
func EffectiveWorkdir(id Identity, sessionKey, rawWorkdir string) string {
	rawWorkdir = strings.TrimSpace(rawWorkdir)
	if rawWorkdir == "" || rawWorkdir == "." {
		return id.ProjectDir(sessionKey)
	}
	// Preserve the project directory name from the real path, but mount it
	// under the virtual home. e.g. /real/path/my-app → /Users/alex/projects/my-app
	base := filepath.Base(rawWorkdir)
	if base == "." || base == "/" {
		return id.ProjectDir(sessionKey)
	}
	return filepath.Join(id.HomeDir, "projects", base)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// --- name pools ---

var usernames = []string{
	"alexchen", "sarah_kim", "michael_jones", "emma_liu", "david_park",
	"olivia_wang", "james_singh", "sophia_lee", "daniel_wu", "isabella_zhang",
	"william_patel", "mia_nguyen", "benjamin_garcia", "charlotte_li", "lucas_martinez",
	"amelia_yang", "henry_anderson", "evelyn_taylor", "alexander_thomas", "harper_brown",
	"sebastian_moore", "aria_jackson", "jack_wilson", "scarlett_white", "owen_harris",
	"luna_martin", "liam_thompson", "chloe_robinson", "noah_clark", "riley_lewis",
	"ethan_walker", "zoey_hall", "mason_allen", "lily_young", "logan_king",
	"grace_wright", "aiden_scott", "nora_green", "elijah_adams", "layla_baker",
	"caleb_nelson", "aurora_hill", "ryan_campbell", "penelope_mitchell", "nathan_roberts",
	"stella_carter", "dylan_phillips", "hazel_evans", "gabriel_turner", "violet_collins",
	"adrian_murphy", "elena_reed", "julian_brooks", "maya_howard", "miles_sanders",
	"clara_foster", "leo_ross", "ivy_cox", "aaron_ward", "piper_rogers",
	"xavier_morgan", "ruby_price", "ivan_bell", "alice_wood", "felix_hayes",
}

var projectNames = []string{
	"webapp-frontend", "api-service", "data-pipeline", "auth-server", "mobile-app",
	"cli-tools", "dashboard", "notification-service", "payment-gateway", "search-engine",
	"user-portal", "config-manager", "analytics-backend", "file-processor", "task-scheduler",
	"chat-service", "inventory-system", "logging-service", "cache-layer", "deploy-scripts",
	"ml-pipeline", "docs-site", "monitoring", "etl-worker", "gateway-proxy",
	"admin-panel", "email-service", "report-generator", "queue-worker", "sync-agent",
	"infra-terraform", "sdk-client", "test-harness", "webhook-handler", "cron-jobs",
	"media-service", "billing-module", "feature-flags", "migration-tool", "load-balancer",
	"graphql-api", "websocket-server", "content-manager", "oauth-provider", "rate-limiter",
	"event-bus", "backup-agent", "cdn-manager", "health-checker", "secret-vault",
	"my-project", "personal-site", "side-project", "prototype-v2", "hackathon-2025",
	"open-source-lib", "blog-engine", "todo-app", "weather-api", "game-server",
	fmt.Sprintf("project-%d", 1), fmt.Sprintf("project-%d", 2),
	fmt.Sprintf("project-%d", 3), fmt.Sprintf("project-%d", 4),
}
