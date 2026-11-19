//go:build unix || windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"time"
)

func compareClients(src source, nimget, aria, curl, baseline string, connections, ariaConnections, rounds int) {
	dir := os.TempDir()
	clients := []func() result{
		func() result {
			return runNimget(nimget, src, dir, fmt.Sprintf("NimGet (%d)", connections), connections)
		},
		func() result {
			return runAria(aria, src, dir, fmt.Sprintf("aria2 (%d)", ariaConnections), ariaConnections)
		},
		func() result { return runCurl(curl, src, dir) },
	}
	if baseline != "" {
		clients = append(clients, func() result {
			return runNimget(baseline, src, dir, fmt.Sprintf("NimGet baseline (%d)", connections), connections)
		})
	}

	samples := make([][]result, len(clients))
	for round := range rounds {
		for i := range clients {
			index := (round + i) % len(clients)
			r := clients[index]()
			if !r.correct {
				check(fmt.Errorf("round %d, %s: output differs from the source", round+1, r.name))
			}
			samples[index] = append(samples[index], r)
			fmt.Printf("Round %d  %s  %.2f s  %.2f MiB/s\n", round+1, r.name, r.wall.Seconds(), r.mbps())
		}
	}
	fmt.Println("\n| Client (connections) | Median seconds | Median MiB/s | SHA-256 verified |")
	fmt.Println("| --- | ---: | ---: | --- |")
	for _, runs := range samples {
		times := make([]time.Duration, 0, len(runs))
		for _, r := range runs {
			times = append(times, r.wall)
		}
		slices.Sort(times)
		mid := len(times) / 2
		wall := times[mid]
		if len(times)%2 == 0 {
			wall = times[mid-1] + (wall-times[mid-1])/2
		}
		r := result{bytes: src.size, wall: wall}
		fmt.Printf("| %s | %.2f | %.2f | %d/%d |\n", runs[0].name, wall.Seconds(), r.mbps(), len(runs), rounds)
	}
}

func runCurl(bin string, src source, dir string) result {
	return mustMeasure("curl (1)", src, dir, func(out string) *exec.Cmd {
		return exec.Command(bin, "--disable", "--noproxy", "127.0.0.1", "--fail", "--silent", "--show-error", "--location", "--output", out, src.url)
	})
}
