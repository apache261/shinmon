package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type result struct {
	Requests   int     `json:"requests"`
	Success    int64   `json:"success"`
	Errors     int64   `json:"errors"`
	Throughput float64 `json:"requestsPerSecond"`
	P50MS      float64 `json:"p50Ms"`
	P95MS      float64 `json:"p95Ms"`
	P99MS      float64 `json:"p99Ms"`
}

func main() {
	var target string
	var baseline string
	var requests, concurrency, portMin, portMax int
	var timeout time.Duration
	flag.StringVar(&target, "target-template", "http://127.0.0.1:%d/", "fmt template receiving the listener port")
	flag.StringVar(&baseline, "baseline-url", "", "optional direct-upstream baseline")
	flag.IntVar(&requests, "requests", 2000, "total gateway requests")
	flag.IntVar(&concurrency, "concurrency", 20, "concurrent workers")
	flag.IntVar(&portMin, "port-min", 4100, "first listener port")
	flag.IntVar(&portMax, "port-max", 4199, "last listener port")
	flag.DurationVar(&timeout, "timeout", 5*time.Second, "per-request timeout")
	flag.Parse()
	key := os.Getenv("SHINMON_LOAD_API_KEY")
	if key == "" || requests < 1 || concurrency < 1 || portMin < 1 || portMax < portMin {
		fmt.Fprintln(os.Stderr, "valid options and SHINMON_LOAD_API_KEY are required")
		os.Exit(2)
	}
	measurement := run(target, key, requests, concurrency, portMin, portMax, timeout)
	output := map[string]any{"gateway": measurement}
	if baseline != "" {
		output["baseline"] = runFixed(baseline, requests, concurrency, timeout)
		output["gatewayAddedP95Ms"] = measurement.P95MS - output["baseline"].(result).P95MS
		output["gatewayAddedP99Ms"] = measurement.P99MS - output["baseline"].(result).P99MS
	}
	_ = json.NewEncoder(os.Stdout).Encode(output)
	if measurement.Errors > 0 || measurement.P95MS > 100 || measurement.P99MS > 250 || measurement.Throughput < 100 {
		os.Exit(1)
	}
}

func run(template, key string, count, concurrency, minPort, maxPort int, timeout time.Duration) result {
	return measure(count, concurrency, func(index int) *http.Request {
		request, _ := http.NewRequest(http.MethodGet, fmt.Sprintf(template, minPort+index%(maxPort-minPort+1)), nil)
		request.Header.Set("X-API-Key", key)
		return request
	}, timeout)
}

func runFixed(url string, count, concurrency int, timeout time.Duration) result {
	return measure(count, concurrency, func(int) *http.Request { request, _ := http.NewRequest(http.MethodGet, url, nil); return request }, timeout)
}

func measure(count, concurrency int, request func(int) *http.Request, timeout time.Duration) result {
	client := &http.Client{Timeout: timeout}
	jobs := make(chan int)
	durations := make([]time.Duration, count)
	var success, failures atomic.Int64
	var wait sync.WaitGroup
	started := time.Now()
	for range concurrency {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for index := range jobs {
				begin := time.Now()
				response, err := client.Do(request(index))
				durations[index] = time.Since(begin)
				if err == nil {
					_, _ = io.Copy(io.Discard, response.Body)
					_ = response.Body.Close()
				}
				if err == nil && response.StatusCode >= 200 && response.StatusCode < 400 {
					success.Add(1)
				} else {
					failures.Add(1)
				}
			}
		}()
	}
	for index := range count {
		jobs <- index
	}
	close(jobs)
	wait.Wait()
	elapsed := time.Since(started)
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	return result{Requests: count, Success: success.Load(), Errors: failures.Load(), Throughput: float64(count) / elapsed.Seconds(), P50MS: milliseconds(percentile(durations, .50)), P95MS: milliseconds(percentile(durations, .95)), P99MS: milliseconds(percentile(durations, .99))}
}

func percentile(values []time.Duration, quantile float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	index := int(float64(len(values)-1)*quantile + .5)
	return values[index]
}
func milliseconds(value time.Duration) float64 { return float64(value) / float64(time.Millisecond) }
