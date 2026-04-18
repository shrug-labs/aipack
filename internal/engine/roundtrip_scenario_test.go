package engine

import (
	"strings"
	"testing"
)

// TestScenario_CodexApprovalModePreservedAcrossSync simulates the round-trip
// the user described:
//  1. Sync #1 renders mcp_servers with enabled_tools, NO approval_mode.
//  2. User interacts with Codex; Codex auto-approves get_pr, writing
//     [mcp_servers.github.tools.get_pr] approval_mode = "approve".
//  3. Sync #2 runs with unchanged profile (still no always_allowed_tools).
//     The three-way merge should preserve the user-added tools table.
func TestScenario_CodexApprovalModePreservedAcrossSync(t *testing.T) {
	t.Parallel()

	// Step 1: what sync #1 rendered (prev-managed overlay in ledger).
	sync1Managed := []byte(`
[mcp_servers.github]
enabled = true
command = "node"
args = ["github.js"]
enabled_tools = ["get_pr", "list_repos"]
startup_timeout_sec = 10
`)

	// Step 2: disk state after Codex added approval_mode.
	onDisk := []byte(`
[mcp_servers.github]
enabled = true
command = "node"
args = ["github.js"]
enabled_tools = ["get_pr", "list_repos"]
startup_timeout_sec = 10

[mcp_servers.github.tools.get_pr]
approval_mode = "approve"
`)

	// Step 3: sync #2 renders identical managed content (profile unchanged).
	sync2Managed := sync1Managed

	merged, ops, err := threeWayMergeTOML(onDisk, sync1Managed, sync2Managed)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	mergedStr := string(merged)
	if !strings.Contains(mergedStr, "approval_mode") {
		t.Errorf("approval_mode stanza was stomped by sync #2:\n%s\n\nops: %v", mergedStr, ops)
	}
	if !strings.Contains(mergedStr, "get_pr") {
		t.Errorf("tools.get_pr table was stomped:\n%s", mergedStr)
	}
	if !strings.Contains(mergedStr, "enabled_tools") {
		t.Errorf("expected managed enabled_tools still present:\n%s", mergedStr)
	}
	t.Logf("merged output:\n%s\nmerge ops: %v", mergedStr, ops)
}

// TestScenario_CodexApprovalModeSurvivesProfileToolAddition simulates the
// user adding a NEW tool to allowed_tools between syncs. The user's earlier
// approval_mode on an unrelated tool must still survive.
func TestScenario_CodexApprovalModeSurvivesProfileToolAddition(t *testing.T) {
	t.Parallel()
	sync1Managed := []byte(`
[mcp_servers.github]
enabled = true
command = "node"
enabled_tools = ["get_pr"]
startup_timeout_sec = 10
`)
	onDisk := []byte(`
[mcp_servers.github]
enabled = true
command = "node"
enabled_tools = ["get_pr"]
startup_timeout_sec = 10

[mcp_servers.github.tools.get_pr]
approval_mode = "approve"
`)
	// Sync #2: profile adds list_repos to allowed_tools.
	sync2Managed := []byte(`
[mcp_servers.github]
enabled = true
command = "node"
enabled_tools = ["get_pr", "list_repos"]
startup_timeout_sec = 10
`)

	merged, ops, err := threeWayMergeTOML(onDisk, sync1Managed, sync2Managed)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	mergedStr := string(merged)
	if !strings.Contains(mergedStr, "approval_mode") {
		t.Errorf("approval_mode stomped when managed set grew:\n%s\n\nops: %v", mergedStr, ops)
	}
	if !strings.Contains(mergedStr, "list_repos") {
		t.Errorf("new managed tool missing:\n%s", mergedStr)
	}
	t.Logf("merged output:\n%s\nmerge ops: %v", mergedStr, ops)
}
