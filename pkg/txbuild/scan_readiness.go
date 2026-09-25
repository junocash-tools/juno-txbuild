package txbuild

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Abdullah1738/juno-sdk-go/junoscan"
)

type blockHashReader interface {
	GetBlockHash(context.Context, int64) (string, error)
}

type scannerAnchorSnapshot struct {
	height int64
	hash   string
}

func captureScannerAnchor(ctx context.Context, rpc blockHashReader, sc scannerHealthReader, nodeHeight int64) (scannerAnchorSnapshot, error) {
	if sc == nil {
		return scannerAnchorSnapshot{}, errors.New("txbuild: scanner is nil")
	}
	snapshot, err := captureNodeAnchor(ctx, rpc, nodeHeight)
	if err != nil {
		return scannerAnchorSnapshot{}, err
	}

	health, err := sc.Health(ctx)
	if err != nil {
		return scannerAnchorSnapshot{}, err
	}
	if err := requireScannerHealthAtAnchor(health, snapshot.height, snapshot.hash); err != nil {
		return scannerAnchorSnapshot{}, err
	}
	return snapshot, nil
}

func verifyScannerAnchor(ctx context.Context, rpc blockHashReader, sc scannerHealthReader, snapshot scannerAnchorSnapshot) error {
	if sc == nil {
		return errors.New("txbuild: scanner is nil")
	}
	if err := verifyNodeAnchor(ctx, rpc, snapshot); err != nil {
		return err
	}

	health, err := sc.Health(ctx)
	if err != nil {
		return err
	}
	return requireScannerHealthAtAnchor(health, snapshot.height, snapshot.hash)
}

func captureNodeAnchor(ctx context.Context, rpc blockHashReader, nodeHeight int64) (scannerAnchorSnapshot, error) {
	if rpc == nil {
		return scannerAnchorSnapshot{}, errors.New("txbuild: rpc is nil")
	}
	if nodeHeight < 0 {
		return scannerAnchorSnapshot{}, errors.New("txbuild: invalid node height")
	}
	nodeHash, err := rpc.GetBlockHash(ctx, nodeHeight)
	if err != nil {
		return scannerAnchorSnapshot{}, err
	}
	nodeHash = strings.TrimSpace(nodeHash)
	if nodeHash == "" {
		return scannerAnchorSnapshot{}, errors.New("txbuild: node block hash is empty")
	}
	return scannerAnchorSnapshot{height: nodeHeight, hash: nodeHash}, nil
}

func verifyNodeAnchor(ctx context.Context, rpc blockHashReader, snapshot scannerAnchorSnapshot) error {
	if rpc == nil {
		return errors.New("txbuild: rpc is nil")
	}
	if snapshot.height < 0 || strings.TrimSpace(snapshot.hash) == "" {
		return errors.New("txbuild: invalid node anchor snapshot")
	}
	nodeHash, err := rpc.GetBlockHash(ctx, snapshot.height)
	if err != nil {
		return err
	}
	nodeHash = strings.TrimSpace(nodeHash)
	if nodeHash == "" {
		return errors.New("txbuild: node block hash is empty")
	}
	if nodeHash != snapshot.hash {
		return errors.New("txbuild: node anchor hash changed during planning")
	}
	return nil
}

func requireScannerHealthAtAnchor(health junoscan.HealthResponse, nodeHeight int64, nodeHash string) error {
	if strings.TrimSpace(health.Status) != "ok" {
		return fmt.Errorf("txbuild: scanner health status is %q, want ok", strings.TrimSpace(health.Status))
	}
	if health.ScannedHeight == nil {
		return errors.New("txbuild: scanner health missing scanned_height")
	}
	if *health.ScannedHeight != nodeHeight {
		return fmt.Errorf("txbuild: scanner height mismatch: node=%d scanner=%d", nodeHeight, *health.ScannedHeight)
	}
	if health.ScannedHash == nil || strings.TrimSpace(*health.ScannedHash) == "" {
		return errors.New("txbuild: scanner health missing scanned_hash")
	}
	if strings.TrimSpace(*health.ScannedHash) != nodeHash {
		return errors.New("txbuild: scanner hash mismatch")
	}
	return nil
}

func orchardWitnessAtAnchor(ctx context.Context, sc *junoscan.Client, anchorHeight int64, positions []uint32) (junoscan.OrchardWitnessResponse, error) {
	if sc == nil {
		return junoscan.OrchardWitnessResponse{}, errors.New("txbuild: scanner is nil")
	}
	wit, err := sc.OrchardWitness(ctx, &anchorHeight, positions)
	if err != nil {
		return junoscan.OrchardWitnessResponse{}, err
	}
	if strings.TrimSpace(wit.Status) != "ok" {
		return junoscan.OrchardWitnessResponse{}, fmt.Errorf("txbuild: scanner witness status is %q, want ok", strings.TrimSpace(wit.Status))
	}
	if wit.AnchorHeight != anchorHeight {
		return junoscan.OrchardWitnessResponse{}, fmt.Errorf("txbuild: scanner witness anchor_height mismatch: requested=%d returned=%d", anchorHeight, wit.AnchorHeight)
	}
	return wit, nil
}
