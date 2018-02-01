package cli

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"context"
	"github.com/openconfig/gnmi/client"
)

type record struct {
	rts []time.Time //request time stamp
	sts []time.Time //sync time stamp
}
type groupRecord struct {
	grMap map[uint]*record //record for one session group
}

// displayGraphResults collect request/response data and display them graphically
func displayGraphResults(ctx context.Context, query client.Query, cfg *Config) error {
	// For each concurrent "session number" group
	var groupRecordMap = make(map[uint]groupRecord)

	ms := cfg.ConcurrentMax
	if ms < cfg.Concurrent {
		ms = cfg.Concurrent
	}
	var grp uint
	for grp = cfg.Concurrent; grp <= ms; {
		grMap := make(map[uint]*record)
		groupRecordMap[grp] = groupRecord{grMap}
		grp *= 2
		if grp > ms && grp != 2*ms {
			grp = ms
		}
	}

	var w sync.WaitGroup
	for sessionGrp, grd := range groupRecordMap {
		cfg.Display([]byte(fmt.Sprintf("%v sessionGrp:%v  cfg.Count %v \n", time.Now().String(), sessionGrp, cfg.Count)))
		for sessionNo := uint(1); sessionNo <= sessionGrp; sessionNo++ {
			rts := []time.Time{}
			sts := []time.Time{}
			rd := &record{rts: rts, sts: sts}
			go func() {
				w.Add(1)
				defer w.Done()
				c := client.New()
				defer c.Close()

				complete := false

				query.NotificationHandler = func(n client.Notification) error {
					switch v := n.(type) {
					case client.Update:
					case client.Delete:
					case client.Sync:
						rd.sts = append(rd.sts, time.Now())
						complete = true
					case client.Error:
						cfg.Display([]byte(fmt.Sprintf("Error: %v", v)))
					}
					return nil
				}
				rd.rts = append(rd.rts, time.Now())
				if err := c.Subscribe(ctx, query, cfg.ClientTypes...); err != nil {
					cfg.Display([]byte(fmt.Sprintf("client had error while displaying results:\n\t%v", err)))
					return
				}
				header := false
				for count := cfg.Count; count > 0; count-- {
					rd.rts = append(rd.rts, time.Now())
					if err := c.Poll(); err != nil {
						cfg.Display([]byte(fmt.Sprintf("client.Poll(): %v", err)))
						return
					}
					if !header {
						header = true
					}
					if count > 0 {
						time.Sleep(cfg.PollingInterval)
					}
				}
			}()
			grd.grMap[sessionNo] = rd
		}
		w.Wait()
		cfg.Display([]byte(fmt.Sprintf("%v done with sessionGrp:sessionNo %v \n", time.Now().String(), sessionGrp)))
	}

	sgrps := make([]int, 0)
	for sessionGrp, _ := range groupRecordMap {
		sgrps = append(sgrps, int(sessionGrp))
	}
	sort.Ints(sgrps)
	for _, sessionGrp := range sgrps {
		grd := groupRecordMap[uint(sessionGrp)]
		cfg.Display([]byte(fmt.Sprintf("len(grd): %v\n", len(grd.grMap))))
		for sessionNo, rd := range grd.grMap {
			cfg.Display([]byte(fmt.Sprintf("sessionGrp:sessionNo (%v:%v) %#v\n", sessionGrp, sessionNo, rd)))
		}
	}
	return nil
}
