package domain

import (
	"errors"
	"testing"
)

func TestDeploymentHappyPath(t *testing.T) {
	path := []DeploymentStatus{
		DeploymentQueued,
		DeploymentCloning,
		DeploymentBuilding,
		DeploymentPushing,
		DeploymentDeploying,
		DeploymentVerifying,
		DeploymentLive,
	}
	for i := 0; i < len(path)-1; i++ {
		if err := ValidateTransition(path[i], path[i+1]); err != nil {
			t.Fatalf("transition %s -> %s: %v", path[i], path[i+1], err)
		}
	}
}

func TestDeploymentCanSkipPushForLocalImages(t *testing.T) {
	if err := ValidateTransition(DeploymentBuilding, DeploymentDeploying); err != nil {
		t.Fatalf("local image transition: %v", err)
	}
}

func TestRollbackCanStartAtDeploying(t *testing.T) {
	if err := ValidateTransition(DeploymentQueued, DeploymentDeploying); err != nil {
		t.Fatalf("rollback transition: %v", err)
	}
}

func TestDeploymentRejectsInvalidAndTerminalTransitions(t *testing.T) {
	tests := []struct{ from, to DeploymentStatus }{
		{DeploymentQueued, DeploymentLive},
		{DeploymentBuilding, DeploymentCloning},
		{DeploymentLive, DeploymentFailed},
		{DeploymentFailed, DeploymentQueued},
		{DeploymentCancelled, DeploymentCloning},
	}
	for _, test := range tests {
		if err := ValidateTransition(test.from, test.to); !errors.Is(err, ErrInvalidTransition) {
			t.Errorf("transition %s -> %s error = %v", test.from, test.to, err)
		}
	}
}

func TestTerminalStatuses(t *testing.T) {
	for _, status := range []DeploymentStatus{DeploymentLive, DeploymentFailed, DeploymentCancelled} {
		if !status.IsTerminal() {
			t.Errorf("%s should be terminal", status)
		}
	}
	if DeploymentVerifying.IsTerminal() {
		t.Error("VERIFYING should not be terminal")
	}
}
