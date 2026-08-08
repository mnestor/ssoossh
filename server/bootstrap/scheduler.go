package bootstrap

// registerJobs walks every field of a.svc and calls RegisterJob on each one
// that implements jobRegistrar, so services can register their own
// scheduled jobs without a hardcoded list that has to be kept in sync as
// services are added or removed.
func (a *app) registerJobs() error {
	// err := a.svc.namsQueue.RegisterJob(a.scheduler)
	// if err != nil {
	// 	return fmt.Errorf("failed to register job for NamsQueueService: %w", err)
	// }

	return nil
}
