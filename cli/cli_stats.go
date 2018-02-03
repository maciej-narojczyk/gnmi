package cli

import (
	"fmt"
	"sync"
	"time"

	"context"
	stats "github.com/openconfig/gnmi/cli/stats"
	"github.com/openconfig/gnmi/client"
)

type groupRecord struct {
	grMap map[uint]*stats.Record //record for one session group
}

// displayGraphResults collect request/response data and display them graphically
// Assuming poll request
func displayGraphResults(ctx context.Context, query client.Query, cfg *Config) error {
	// For each concurrent "session number" group
	var groupRecordMap = make(map[uint]groupRecord)

	ms := cfg.ConcurrentMax
	if ms < cfg.Concurrent {
		ms = cfg.Concurrent
	}

	var grps []uint
	for grp := cfg.Concurrent; grp <= ms; {
		grps = append(grps, grp)

		grp *= 2
		if grp > ms && grp != 2*ms {
			grp = ms
		}
	}

	query.NotificationHandler = func(n client.Notification) error {
		switch v := n.(type) {
		case client.Update:
		case client.Delete:
		case client.Sync:
		case client.Error:
			cfg.Display([]byte(fmt.Sprintf("Error: %v", v)))
		}
		return nil
	}

	for _, grp := range grps {
		grMap := make(map[uint]*stats.Record)
		groupRecordMap[grp] = groupRecord{grMap}

		cfg.Display([]byte(fmt.Sprintf("%v sessionGrp %v ", time.Now().String(), grp)))
		var gMu sync.Mutex
		var w sync.WaitGroup
		for sessionNo := uint(1); sessionNo <= grp; sessionNo++ {
			// cfg.Display([]byte(fmt.Sprintf("%v session %v ", time.Now().String(), sessionNo)))
			w.Add(1)
			go func(grp uint, sessionNo uint) {
				//sessionCtx, cancel := context.WithCancel(context.Background())
				//defer cancel()
				// cfg.Display([]byte(fmt.Sprintf("start grp:sessionNo %v:%v , grMap : %v", grp, sessionNo, grMap)))

				defer w.Done()
				c, _ := stats.NewStatsClient(ctx, query.Destination())
				defer c.Close()

				if err := c.Subscribe(ctx, query); err != nil {
					cfg.Display([]byte(fmt.Sprintf("client had error while Subscribe: %v", err)))
					return
				}
				if err := c.RecvAll(); err != nil {
					if err != nil {
						cfg.Display([]byte(fmt.Sprintf("client had error while Subscribe Recv: %v", err)))
						return
					}

				}
				for count := cfg.Count; count > 0; count-- {
					time.Sleep(cfg.PollingInterval)
					if err := c.Poll(); err != nil {
						cfg.Display([]byte(fmt.Sprintf("client.Poll(): %v", err)))
						return
					}
					if err := c.RecvAll(); err != nil {
						if err != nil {
							cfg.Display([]byte(fmt.Sprintf("client had error while Poll Recv: %v", err)))
							return
						}
					}
				}
				gMu.Lock()
				grMap[sessionNo] = &c.Rd
				gMu.Unlock()
			}(grp, sessionNo)
		}
		w.Wait()
		cfg.Display([]byte(fmt.Sprintf("%v sessionGrp %v done cfg.Count %v\n", time.Now().String(), grp, cfg.Count)))
		time.Sleep(time.Second)
	}

	for _, grp := range grps {
		grd := groupRecordMap[uint(grp)]
		cfg.Display([]byte(fmt.Sprintf("\t len(grd): %v, grp %v\n", len(grd.grMap), grp)))

		for sessionNo := uint(1); sessionNo <= grp; sessionNo++ {

			Rd, ok := grd.grMap[sessionNo]
			if !ok {
				cfg.Display([]byte(fmt.Sprintf("sessionGrp:sessionNo (%v:%v) len is 0\n",
					grp, sessionNo)))
				continue
			}
			if len(Rd.Sts) != len(Rd.Rts) || len(Rd.Sts) == 0 {
				cfg.Display([]byte(fmt.Sprintf("sessionGrp:sessionNo (%v:%v)  garbage records: len(Rts):len(Sts) %d:%d\n",
					grp, sessionNo, len(Rd.Rts), len(Rd.Sts))))
				continue
			}
			var diff time.Duration
			var validCnt int64
			for i := 0; i < len(Rd.Sts); i++ {
				if Rd.Rts[i].After(Rd.Sts[i]) || Rd.Rts[i].Equal(Rd.Sts[i]) {
					continue
				}
				validCnt++
				diff += Rd.Sts[i].Sub(Rd.Rts[i])
			}
			ms := int64(diff / time.Millisecond)
			ms /= validCnt
			//ms = ms / int64(len(Rd.Sts))
			cfg.Display([]byte(fmt.Sprintf("sessionGrp:sessionNo (%v:%v) latency %v ms\n", grp, sessionNo, ms)))
		}
	}
	return nil
}
