package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type record struct {
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Service   string `json:"service"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

func main() {
	format := flag.String("format", "jsonl", "jsonl or plain")
	entries := flag.Int("entries", 100_000, "number of entries")
	output := flag.String("output", "./samples/generated.jsonl", "output path")
	errorRate := flag.Float64("error-rate", 0.02, "fraction of error entries")
	malformedRate := flag.Float64("malformed-rate", 0.001, "fraction of malformed JSON entries")
	seed := flag.Uint64("seed", 42, "deterministic seed")
	flag.Parse()
	if *entries < 1 || (*format != "jsonl" && *format != "plain") {
		fmt.Fprintln(os.Stderr, "entries must be positive and format must be jsonl or plain")
		os.Exit(2)
	}
	if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
		panic(err)
	}
	file, err := os.Create(*output)
	if err != nil {
		panic(err)
	}
	defer file.Close()
	writer := bufio.NewWriterSize(file, 1<<20)
	defer writer.Flush()

	random := rand.New(rand.NewPCG(*seed, *seed^0x9e3779b97f4a7c15))
	services := []string{"gateway", "api", "payment", "worker", "notifications"}
	messages := []string{
		"Request accepted", "Database query completed", "Cache entry refreshed",
		"Job scheduled", "Provider response received", "User session validated",
	}
	base := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	encoder := json.NewEncoder(writer)
	for index := 0; index < *entries; index++ {
		level := "INFO"
		message := messages[random.IntN(len(messages))]
		if random.Float64() < *errorRate || (index%50_000 >= 40_000 && index%50_000 < 40_100) {
			level = "ERROR"
			message = "Payment provider timeout"
			if index%19 == 0 {
				message += "\njava.lang.RuntimeException: upstream timeout\n    at PaymentService.charge(PaymentService.java:42)"
			}
		} else if random.IntN(20) == 0 {
			level = "WARN"
			message = "Retry scheduled after transient failure"
		} else if random.IntN(30) == 0 {
			level = "DEBUG"
		}
		if index%25_000 == 0 && index > 0 {
			message += " " + strings.Repeat("payload-", 4096)
		}
		item := record{
			Timestamp: base.Add(time.Duration(index) * 125 * time.Millisecond).Format(time.RFC3339Nano),
			Level:     level,
			Service:   services[random.IntN(len(services))],
			Message:   message,
			RequestID: fmt.Sprintf("req-%08d", index/6),
		}
		if *format == "jsonl" {
			if random.Float64() < *malformedRate {
				fmt.Fprintf(writer, `{"timestamp":%q,"level":%q,"message":`, item.Timestamp, item.Level)
				fmt.Fprintln(writer, item.Message)
			} else if err := encoder.Encode(item); err != nil {
				panic(err)
			}
		} else {
			fmt.Fprintf(writer, "%s %s [%s] request=%s %s\n",
				item.Timestamp, item.Level, item.Service, item.RequestID, strings.ReplaceAll(item.Message, "\n", "\\n"))
		}
	}
	fmt.Printf("generated %d %s entries at %s\n", *entries, *format, *output)
}
