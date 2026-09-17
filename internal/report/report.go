package report

import "github.com/ctej-codes/terraform-drift-checker/internal/models"

type Report struct {
    ExpectedCount int           `json:"expected_count"`
    ActualCount   int           `json:"actual_count"`
    Diffs         []models.Diff `json:"diffs"`
}
