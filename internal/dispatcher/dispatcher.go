package dispatcher

import (
	"context"
	"log"
	"strconv"
	"time"

	"github.com/igorMSoares/rinha-de-backend-2025/internal/balancer"
	"github.com/igorMSoares/rinha-de-backend-2025/internal/utils"
	"github.com/igorMSoares/rinha-de-backend-2025/internal/worker"
	"github.com/redis/go-redis/v9"
)

const workersLimit = 20

type WorkDispatcher struct {
	workerPool     chan chan *worker.Work
	workers        [workersLimit]*worker.Worker
	workQueueKey   string
	loadBalancer   *balancer.LoadBalancer
	redisClient    *redis.Client
	circuitTimeout time.Duration
}

func NewWorkDispatcher(lb *balancer.LoadBalancer, rc *redis.Client, rh *worker.ResultsHandler) *WorkDispatcher {
	maxWorkers, _ := strconv.Atoi(utils.Getenv("MAX_WORKERS", "10"))
	if maxWorkers > workersLimit {
		maxWorkers = workersLimit
	}

	circuitTimeout, _ := time.ParseDuration(utils.Getenv("LB_CIRCUIT_TIMEOUT", "500ms"))

	wd := &WorkDispatcher{
		workQueueKey:   "work_queue",
		workerPool:     make(chan chan *worker.Work, maxWorkers),
		loadBalancer:   lb,
		redisClient:    rc,
		circuitTimeout: circuitTimeout,
	}

	ws := worker.NewWorkStore()

	for i := range maxWorkers {
		worker := worker.NewWorker(
			i,
			wd.workerPool,
			wd.workQueueKey,
			ws,
			lb,
			rc,
			rh,
		)

		wd.workers[i] = worker
		worker.Start()
	}
	log.Printf("%d workers have been registered\n", maxWorkers)

	return wd
}

func (wd *WorkDispatcher) Start() {
	log.Println("Starting dispatcher")
	go func() {
		for {
			if !wd.loadBalancer.AllowWork() {
				log.Printf("Dispatcher will sleep for %v: Load balancer circuit is open", wd.circuitTimeout)
				time.Sleep(wd.circuitTimeout)
				continue
			}

			res, err := wd.redisClient.BLPop(context.Background(), 1*time.Second, wd.workQueueKey).Result()
			if err != nil {
				if err != redis.Nil {
					log.Printf("Redis error when consuming from work_queue. error: %s\n", err)
				}
				continue
			}

			payload := []byte(res[1])

			// bloqueia até ter algum worker disponível
			chWorker := <-wd.workerPool

			chWorker <- &worker.Work{
				Raw: payload,
			}

		}
	}()
}
