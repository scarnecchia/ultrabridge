package booxpipeline

import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

// Processor manages the Boox notes processing pipeline.
type Processor struct {
	store     *Store
	cfg       WorkerConfig
	notesPath string
	logger    *slog.Logger
	cancel    context.CancelFunc
	done      chan struct{}
}

// New creates a new Boox processor.
func New(db *sql.DB, notesPath string, cfg WorkerConfig, logger *slog.Logger) *Processor {
	return &Processor{
		store:     NewStore(db),
		cfg:       cfg,
		notesPath: notesPath,
		logger:    logger,
		done:      make(chan struct{}),
	}
}

// Enqueue adds a .note file to the processing queue.
func (p *Processor) Enqueue(ctx context.Context, absPath string) error {
	return p.store.EnqueueJob(ctx, absPath)
}

// Start begins the worker loop and watchdog.
func (p *Processor) Start(ctx context.Context) error {
	ctx, p.cancel = context.WithCancel(ctx)
	go p.run(ctx)
	go p.watchdog(ctx)
	return nil
}

// Stop signals shutdown and waits for the worker to finish.
func (p *Processor) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
	<-p.done
}

func (p *Processor) run(ctx context.Context) {
	defer close(p.done)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		job, err := p.store.ClaimNextJob(ctx)
		if err != nil {
			p.logger.Error("claim boox job", "error", err)
		}
		if job == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}

		p.processJob(ctx, job)
	}
}

func (p *Processor) processJob(ctx context.Context, job *BooxJob) {
	p.logger.Info("processing boox note", "path", job.NotePath, "job_id", job.ID)

	if err := p.executeJob(ctx, job); err != nil {
		p.logger.Error("boox job failed", "job_id", job.ID, "error", err)
		p.store.FailJob(ctx, job.ID, err.Error())
		return
	}

	ocrSource := "api"
	if p.cfg.OCR == nil {
		ocrSource = ""
	}
	p.store.CompleteJob(ctx, job.ID, ocrSource, "")
	p.logger.Info("boox note processed", "path", job.NotePath, "job_id", job.ID)
}

// watchdog reclaims stuck jobs (in_progress for >10 minutes).
func (p *Processor) watchdog(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.store.ReclaimStuckJobs(ctx, 10*time.Minute)
		}
	}
}
