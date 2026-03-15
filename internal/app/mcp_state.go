package app

import (
	"os"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/harness"
)

func capturedMCPDigests(res harness.CaptureResult) (map[string]string, error) {
	if len(res.MCP) == 0 && len(res.MCPServers) > 0 {
		res.MaterializeCapturedMCP(sourcePathForMCP(res))
	}

	digests := make(map[string]string, len(res.MCP))
	for _, captured := range res.MCP {
		content, err := domain.MCPTrackedBytes(captured.Server)
		if err != nil {
			return nil, err
		}
		key := domain.MCPLedgerKey(captured.HarnessPath, captured.Server.Name)
		digests[key] = domain.SingleFileDigest(content)
	}
	return digests, nil
}

func classifyMCPAction(action domain.MCPAction, current map[string]string, lg domain.Ledger) (domain.DiffKind, error) {
	if _, err := os.Stat(action.ConfigPath); os.IsNotExist(err) {
		return domain.DiffCreate, nil
	} else if err != nil {
		return domain.DiffError, err
	}

	entry, ok := lg.Managed[action.LedgerKey()]
	if !ok {
		return domain.DiffUntracked, nil
	}

	currentDigest, ok := current[action.LedgerKey()]
	if !ok {
		return domain.DiffConflict, nil
	}

	desiredDigest := domain.SingleFileDigest(action.Content)
	if currentDigest == desiredDigest {
		return domain.DiffIdentical, nil
	}
	if currentDigest == entry.Digest {
		return domain.DiffManaged, nil
	}
	return domain.DiffConflict, nil
}
