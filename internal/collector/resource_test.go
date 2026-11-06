package collector

import "testing"

func TestSourceCopyBudgetEnforcesPerUnitFileAndByteLimits(t *testing.T) {
	budget := &sourceCopyBudget{}
	if err := budget.accountRegularFile(maxSourceUnitBytes); err != nil {
		t.Fatalf("exact source-unit byte limit rejected: %v", err)
	}
	if err := budget.accountRegularFile(1); err == nil {
		t.Fatal("source unit exceeded its byte budget without rejection")
	}

	budget.beginUnit()
	for i := 0; i < maxSourceUnitFiles; i++ {
		if err := budget.accountRegularFile(0); err != nil {
			t.Fatalf("source file %d unexpectedly rejected: %v", i, err)
		}
	}
	if err := budget.accountRegularFile(0); err == nil {
		t.Fatal("source unit exceeded its file-count budget without rejection")
	}
}

func TestSourceCopyBudgetEnforcesWorkspaceAggregateLimits(t *testing.T) {
	budget := &sourceCopyBudget{}
	for unit := int64(0); unit < maxWorkspaceBytes/maxSourceUnitBytes; unit++ {
		budget.beginUnit()
		if err := budget.accountRegularFile(maxSourceUnitBytes); err != nil {
			t.Fatalf("source unit %d unexpectedly rejected: %v", unit, err)
		}
	}
	budget.beginUnit()
	if err := budget.accountRegularFile(1); err == nil {
		t.Fatal("workspace exceeded its aggregate byte budget without rejection")
	}

	budget = &sourceCopyBudget{}
	for unit := 0; unit < maxWorkspaceFiles/maxSourceUnitFiles; unit++ {
		budget.beginUnit()
		for file := 0; file < maxSourceUnitFiles; file++ {
			if err := budget.accountRegularFile(0); err != nil {
				t.Fatalf("unit %d file %d unexpectedly rejected: %v", unit, file, err)
			}
		}
	}
	budget.beginUnit()
	if err := budget.accountRegularFile(0); err == nil {
		t.Fatal("workspace exceeded its aggregate file-count budget without rejection")
	}
}

func TestSourceCopyBudgetRejectsNegativeFileSizes(t *testing.T) {
	if err := (&sourceCopyBudget{}).accountRegularFile(-1); err == nil {
		t.Fatal("negative source file size was accepted")
	}
}
