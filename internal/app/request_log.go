package app

import (
	"context"

	"github.com/ARMmaster17/minirouter/internal/domain"
)

type RequestLogStore interface {
	Append(ctx context.Context, entry domain.RequestLogEntry) error
	Recent(limit int) []domain.RequestLogEntry
	Stats() domain.RequestAggregateStats
	Subscribe() (<-chan struct{}, func())
}
