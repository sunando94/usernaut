package controller

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/redhat-data-and-ai/usernaut/internal/controller/periodicjobs"
	"github.com/redhat-data-and-ai/usernaut/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

type PeriodicTasksReconciler struct {
	client.Client
	SnowflakeEnvironment string
	taskManager          *periodicjobs.PeriodicTaskManager
	cacheClient          cache.Cache
}

func NewPeriodicTasksReconciler(
	k8sClient client.Client,
	sharedCacheMutex *sync.RWMutex,
	cacheClient cache.Cache,
) (*PeriodicTasksReconciler, error) {
	periodicTaskManager := periodicjobs.NewPeriodicTaskManager()

	// Add jobs to the periodic task manager
	userOffboardingJob, err := periodicjobs.NewUserOffboardingJob(sharedCacheMutex)

	if err != nil {
		return nil, fmt.Errorf("failed to create user offboarding job: %w", err)
	}
	userOffboardingJob.AddToPeriodicTaskManager(periodicTaskManager)

	return &PeriodicTasksReconciler{
		Client:      k8sClient,
		taskManager: periodicTaskManager,
		cacheClient: cacheClient,
	}, nil
}

// AddToManager will add the reconciler for the configured obj to a manager.
func (ptr *PeriodicTasksReconciler) AddToManager(mgr manager.Manager) error {
	return mgr.Add(ptr)
}

// Start the periodic tasks controller
// not event triggered like a conventional controller
// does not watch any kuberntes resources
// this is the platform through with periodic jobs get passed to controller manager
func (ptr *PeriodicTasksReconciler) Start(ctx context.Context) error {
	logger := log.FromContext(ctx)
	logger.Info("Starting periodic tasks controller")

	defer func() {
		logger.Info("Finishing periodic tasks controller")
	}()

	logger.Info("Initializing periodic tasks controller")

	logger.Info("Periodic tasks controller is enabled. Proceeding with initialization")

	// Wait for dependencies (cache, etc.) to be ready using health checks
	if err := ptr.waitForDependencies(ctx); err != nil {
		logger.Error(err, "Failed to wait for dependencies")
		return err
	}

	logger.Info("Invoking task manager to run all periodic tasks")
	err := ptr.taskManager.RunAll(ctx)
	if err != nil {
		logger.Error(err, "Error occurred while running periodic tasks")
		return err
	}

	logger.Info("All periodic tasks have been started successfully")
	return nil
}

// waitForDependencies waits for all required dependencies to be ready before starting periodic tasks
func (ptr *PeriodicTasksReconciler) waitForDependencies(ctx context.Context) error {
	logger := log.FromContext(ctx)
	logger.Info("Waiting for dependencies to be ready")

	// Check cache health by performing a simple operation
	if err := ptr.waitForCacheHealth(ctx); err != nil {
		return fmt.Errorf("cache health check failed: %w", err)
	}

	logger.Info("All dependencies are ready")
	return nil
}

// waitForCacheHealth performs health checks on the cache to ensure it's ready
func (ptr *PeriodicTasksReconciler) waitForCacheHealth(ctx context.Context) error {
	logger := log.FromContext(ctx)
	logger.Info("Performing cache health check")

	// Perform health check with retries
	maxRetries := 5
	retryDelay := 2 * time.Second

	for i := 0; i < maxRetries; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Try a simple cache operation to verify it's working
		testKey := "health_check_" + fmt.Sprintf("%d", time.Now().Unix())
		testValue := "healthy"

		// Test Set operation
		if err := ptr.cacheClient.Set(ctx, testKey, testValue, 30*time.Second); err != nil {
			logger.Info("Cache health check failed, retrying", "attempt", i+1, "error", err)
			if i == maxRetries-1 {
				return fmt.Errorf("cache set operation failed after %d attempts: %w", maxRetries, err)
			}
			time.Sleep(retryDelay)
			continue
		}

		// Test Get operation
		if _, err := ptr.cacheClient.Get(ctx, testKey); err != nil {
			logger.Info("Cache health check failed, retrying", "attempt", i+1, "error", err)
			if i == maxRetries-1 {
				return fmt.Errorf("cache get operation failed after %d attempts: %w", maxRetries, err)
			}
			time.Sleep(retryDelay)
			continue
		}

		// Clean up test key
		_ = ptr.cacheClient.Delete(ctx, testKey)

		logger.Info("Cache health check passed", "attempt", i+1)
		return nil
	}

	return fmt.Errorf("cache health check failed after %d attempts", maxRetries)
}
