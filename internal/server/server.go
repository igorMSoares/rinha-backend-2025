package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/igorMSoares/rinha-de-backend-2025/internal/worker"

	"github.com/redis/go-redis/v9"
)

type Server struct {
	defaultAmountKey   string
	fallbackAmountKey  string
	defaultCounterKey  string
	fallbackCounterKey string
	workQueueKey       string
	resultsHandler     *worker.ResultsHandler
	redisClient        *redis.Client
}

type decimalAmount float64

func (da decimalAmount) MarshalJSON() ([]byte, error) {
	return fmt.Appendf(nil, "%.2f", da), nil
}

func (da decimalAmount) String() string {
	return fmt.Sprintf("%.2f", da)
}

type PaymentsSummary struct {
	TotalRequests int64         `json:"totalRequests"`
	TotalAmount   decimalAmount `json:"totalAmount"`
}

type SummaryPayload struct {
	Default  PaymentsSummary `json:"default"`
	Fallback PaymentsSummary `json:"fallback"`
}

func NewServer(queuePrefix string, redisClient *redis.Client, resultsHandler *worker.ResultsHandler) *Server {
	return &Server{
		defaultAmountKey:   "amount:default:counter",
		fallbackAmountKey:  "amount:fallback:counter",
		defaultCounterKey:  "default:counter",
		fallbackCounterKey: "fallback:counter",
		workQueueKey:       "work_queue",
		resultsHandler:     resultsHandler,
		redisClient:        redisClient,
	}
}

func (s *Server) EnqueueRequest(reqPayload []byte) error {
	return s.redisClient.RPush(context.Background(), s.workQueueKey, reqPayload).Err()
}

func (s *Server) handleCountCmd(cmd *redis.SliceCmd) (int64, error) {
	result, err := cmd.Result()
	if err != nil {
		return 0, err
	}

	count := int64(0)
	var intVal int64
	for _, val := range result {
		if val == nil {
			continue
		}

		if intVal, err = strconv.ParseInt(val.(string), 10, 64); err != nil {
			return 0, err
		}

		count += intVal
	}

	return count, nil
}

func (s *Server) handleSumCmd(cmd *redis.SliceCmd) (float64, error) {
	result, err := cmd.Result()
	if err != nil {
		return 0.0, err
	}

	sum := 0.0
	var floatVal float64
	for _, val := range result {

		if val == nil {
			continue
		}

		if floatVal, err = strconv.ParseFloat(val.(string), 64); err != nil {
			return 0, err
		}

		sum += floatVal
	}

	return sum, nil
}

func (s *Server) handleSummaryReq(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	paramFrom := r.URL.Query().Get("from")
	paramTo := r.URL.Query().Get("to")

	// log.Printf("from: %s | to: %s", paramFrom, paramTo)

	tsFrom := 0
	tsTo := math.MaxInt64
	tsFromMilli := int64(tsFrom)
	tsToMilli := int64(tsTo)

	if paramFrom != "" {
		parsedTime, err := time.Parse(time.RFC3339Nano, paramFrom)
		if err != nil {
			http.Error(w, "Invalid query param: from", http.StatusBadRequest)
		}

		tsFromMilli = parsedTime.UnixMilli()
		tsFrom = int(tsFromMilli)
	}
	if paramTo != "" {
		parsedTime, err := time.Parse(time.RFC3339Nano, paramTo)
		if err != nil {
			http.Error(w, "Invalid query param: to", http.StatusBadRequest)
		}

		tsToMilli = parsedTime.UnixMilli()
		tsTo = int(tsToMilli)
	}

	ctx := context.Background()

	pipe := s.redisClient.Pipeline()
	defaultSumCmd := s.resultsHandler.ResultsCountByRangePipe(pipe, ctx, s.defaultAmountKey, tsFromMilli, tsToMilli)
	fallbackSumCmd := s.resultsHandler.ResultsCountByRangePipe(pipe, ctx, s.fallbackAmountKey, tsFromMilli, tsToMilli)

	defaultCountCmd := s.resultsHandler.ResultsCountByRangePipe(pipe, ctx, s.defaultCounterKey, tsFromMilli, tsToMilli)
	fallbackCountCmd := s.resultsHandler.ResultsCountByRangePipe(pipe, ctx, s.fallbackCounterKey, tsFromMilli, tsToMilli)

	_, err := pipe.Exec(ctx)
	if err != nil {
		log.Printf("Failed to retrieve payment summary. redis pipeline failed: %v\n", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	summary := SummaryPayload{
		Default:  PaymentsSummary{},
		Fallback: PaymentsSummary{},
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func(cmd *redis.SliceCmd) {
		defer wg.Done()

		count, err := s.handleCountCmd(cmd)
		if err != nil {
			log.Printf("Failed to handle default counter: %v", err)
			return
		}

		summary.Default.TotalRequests = count
	}(defaultCountCmd)

	wg.Add(1)
	go func(cmd *redis.SliceCmd) {
		defer wg.Done()

		count, err := s.handleCountCmd(cmd)
		if err != nil {
			log.Printf("Failed to handle fallback counter: %v", err)
			return
		}

		summary.Fallback.TotalRequests = count
	}(fallbackCountCmd)

	wg.Add(1)
	go func(cmd *redis.SliceCmd) {
		defer wg.Done()

		sum, err := s.handleSumCmd(cmd)
		if err != nil {
			log.Printf("Failed to handle default summary sum: %v\n", err)
			return
		}

		summary.Default.TotalAmount = decimalAmount(sum)
	}(defaultSumCmd)

	wg.Add(1)
	go func(cmd *redis.SliceCmd) {
		defer wg.Done()

		sum, err := s.handleSumCmd(cmd)
		if err != nil {
			log.Printf("Failed to handle fallback summary sum: %v\n", err)
			return
		}

		summary.Fallback.TotalAmount = decimalAmount(sum)
	}(fallbackSumCmd)

	wg.Wait()

	resData, err := json.Marshal(summary)
	if err != nil {
		log.Printf("Failed to parse summary response: %s\n", err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(resData)
}

func (s *Server) handlePaymentReq(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	if r.Method != http.MethodPost {
		return
	}

	payload, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		log.Println("Failed to read request body")
		return
	}

	go func(payload []byte) {
		err = s.EnqueueRequest(payload)
		if err != nil {
			log.Printf("Failed to push request to queue: %s\n", err.Error())
		}
	}(payload)
}

func (s *Server) Start() {
	srv := &http.Server{
		Addr:         ":8081",
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	http.HandleFunc("/payments", s.handlePaymentReq)
	http.HandleFunc("/payments-summary", s.handleSummaryReq)

	log.Println("Server starting on :8081")
	log.Fatal(srv.ListenAndServe())
}
