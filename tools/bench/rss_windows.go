package main

import "os"

const hasPeakRSS = false

func peakRSS(*os.ProcessState) int64 { return 0 }
