package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/igorMSoares/rinha-de-backend-2025/internal/balancer"
	"github.com/igorMSoares/rinha-de-backend-2025/internal/http"
	"github.com/redis/go-redis/v9"
)

const ProcessedQueuePrefix = "processed:"

type workResult struct {
	amountKey   string
	counterKey  string
	timestamp   int64
	amountValue float64
}

type workStore struct {
	sync.RWMutex
	items map[string]bool
}

func NewWorkStore() *workStore {
	return &workStore{
		items: make(map[string]bool),
	}
}

func (s *workStore) contains(key string) bool {
	s.RLock()
	defer s.RUnlock()
	return s.items[key]
}

func (s *workStore) add(key string) {
	s.Lock()
	defer s.Unlock()
	s.items[key] = true
	// log.Printf("%v marked as seen", key)
}

func (s *workStore) remove(key string) {
	s.Lock()
	defer s.Unlock()
	s.items[key] = false
	log.Printf("%v removed from seen", key)
}

type Work struct {
	Payload *WorkPayload
	Raw     []byte
}

type WorkPayload struct {
	CorrelationID string  `json:"correlationId"`
	Amount        float64 `json:"amount"`
	RequestedAt   string  `json:"requestedAt"`
}

type Worker struct {
	ID             int
	chWork         chan *Work
	WorkerPool     chan chan *Work
	workQueueKey   string
	workStore      *workStore
	loadBalancer   *balancer.LoadBalancer
	redisClient    *redis.Client
	resultsHandler *ResultsHandler
}

func NewWorker(id int, wp chan chan *Work, wqk string, ws *workStore, lb *balancer.LoadBalancer, rc *redis.Client, rh *ResultsHandler) *Worker {
	return &Worker{
		ID:             id,
		chWork:         make(chan *Work, 1),
		workQueueKey:   wqk,
		WorkerPool:     wp,
		workStore:      ws,
		loadBalancer:   lb,
		redisClient:    rc,
		resultsHandler: rh,
	}
}

func (w *Worker) handleProcessingFailure(work *Work) {
	w.workStore.remove(work.Payload.CorrelationID)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	if pushErr := w.redisClient.RPush(
		ctx, w.workQueueKey, work.Raw,
	).Err(); pushErr != nil {
		log.Println("Failed to push back to queue on processing failure handler")
	}

	log.Printf("Processing failed for %v: sent back to work queue for retry", work.Payload.CorrelationID)
}

func (w *Worker) publishResult(result *workResult) {
	if err := w.resultsHandler.updateResults(
		context.Background(), result.counterKey, result.amountKey, result.timestamp, result.amountValue,
	); err != nil {
		log.Printf("Failed to publish worker results: %v | result: %v\n", err, result)
	}
}

func (w *Worker) Execute(work *Work) error {
	// log.Printf("Executing worker %v", w.ID)
	timestamp := time.Now().UTC()
	work.Payload.RequestedAt = timestamp.Format("2006-01-02T15:04:05.000Z")

	data, err := json.Marshal(work.Payload)
	if err != nil {
		log.Printf("Failed to marshal work payload. worker: %v\n", w.ID)
		return err
	}

	host, err := w.loadBalancer.MakeRequest(data, nil)
	if err != nil {
		log.Printf("Failed to execute work. worker: %.2d | error: %v\n", w.ID, err.Error())
		return err
	}

	go w.publishResult(&workResult{
		amountKey:   "amount:" + string(host) + ":counter",
		counterKey:  string(host) + ":counter",
		timestamp:   timestamp.UnixMilli(),
		amountValue: work.Payload.Amount,
	})

	return nil
}

func (w *Worker) Start() {
	go func() {
		for {
			// se coloca na workerPool
			w.WorkerPool <- w.chWork

			// bloqueia até chegar algum work
			work := <-w.chWork

			var workPayload WorkPayload
			err := json.Unmarshal(work.Raw, &workPayload)
			if err != nil {
				log.Println("Error processing work: failed to parse payload")
				continue
			}

			if seen := w.workStore.contains(workPayload.CorrelationID); seen {
				log.Printf("Payment already processed: discarding %v", work.Payload.CorrelationID)
				continue
			}

			w.workStore.add(workPayload.CorrelationID)
			work.Payload = &workPayload

			err = w.Execute(work)
			if err != nil {
				if errors.Is(err, http.ErrAlreadyProcessed) {
					continue
				}

				go w.handleProcessingFailure(work)
			}
		}
	}()
}
