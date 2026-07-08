// Code scaffolded by sysgo; edit freely (not regenerated).

package main

import (
	"log"

	"github.com/gaarutyunov/epos/internal/stats"
	gw "github.com/gaarutyunov/epos/internal/stats/adapter/out/gateway"
	"github.com/gaarutyunov/epos/internal/stats/app/usecase"
)

// main is the composition root for the Stats bounded context.
func main() {
	sink := gw.NewStatSinkImpl(stats.New())
	record := usecase.NewRecordPullInteractor(sink)
	read := usecase.NewReadStatisticsInteractor(sink)
	_ = record
	_ = read
	log.Println("Stats: composition root wired (stat-sink port → interactors)")
}
