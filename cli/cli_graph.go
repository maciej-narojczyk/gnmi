package cli

import (
	"context"
	"fmt"
	log "github.com/golang/glog"
	stats "github.com/openconfig/gnmi/cli/stats"
	"github.com/openconfig/gnmi/client"
	//	"strconv"
	"sync"
	"time"

	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/plotutil"
	"gonum.org/v1/plot/vg"
)

type groupRecord struct {
	grMap map[uint]*stats.Record //record for one session group
}

// displayGraphResults collect request/response data and display them graphically
// Assuming poll request
func displayGraphResults(ctx context.Context, query client.Query, cfg *Config) error {
	// For each concurrent "session number" group
	var groupRecordMap = make(map[uint]groupRecord)

	maxS := cfg.ConcurrentMax
	if maxS < cfg.Concurrent {
		maxS = cfg.Concurrent
	}
	//var step uint = 1

	var grps []uint
	for grp := cfg.Concurrent; grp <= maxS; {
		grps = append(grps, grp)

		grp *= 2
		if grp > maxS && grp != 2*maxS {
			grp = maxS
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

		cfg.Display([]byte(fmt.Sprintf("%v sessionGrp %v started ", time.Now().String(), grp)))
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
	//plotGraph("Session Latency", "Session No.", "ms", groupRecordMap)
	if len(grps) > 1 {
		plotGroupsGraph("Session Group Latency", "Session Number", "ms", grps, groupRecordMap)
	} else {
		if cfg.Concurrent > 1 {
			// latency fluctuation among sessions
		} else {
			// latency fluctuation for one session
			groupRecord := groupRecordMap[grps[0]]
			plotSessionGraph("Single Session latency", "Poll No.", "ms", groupRecord.grMap[1])
		}
	}
	return nil
}

//data for one session group
func addSessionPollPoints(rd *stats.Record) plotter.XYs {
	pts := make(plotter.XYs, len(rd.Sts))

	for idx := 0; idx < len(rd.Sts); idx++ {
		diff := rd.Sts[idx].Sub(rd.Rts[idx])
		ms := int64(diff / time.Millisecond)

		x := float64(idx)
		y := float64(ms)
		pts[idx].X = x
		pts[idx].Y = y
	}
	return pts
}

func plotSessionGraph(title, x, y string, rd *stats.Record) error {
	p, err := plot.New()
	if err != nil {
		log.V(1).Infof("plot %v", err)
		return err
	}

	p.Title.Text = title
	p.X.Label.Text = x
	p.Y.Label.Text = y

	err = plotutil.AddLinePoints(p,
		"Poll Session", addSessionPollPoints(rd))
	if err != nil {
		log.V(1).Infof("plotutil.AddLinePoints %v", err)
		return err
	}
	// Save the plot to a PNG file.
	if err := p.Save(16*vg.Inch, 8*vg.Inch, "session_latency.png"); err != nil {
		log.V(1).Infof("save PNG %v", err)
		return err
	}
	return nil
}

// Average latency for this session group
func getGrpLatency(grmap map[uint]*stats.Record) int64 {
	var diff time.Duration
	var sessionCnt int64

	for _, rd := range grmap {
		for i := 0; i < len(rd.Sts); i++ {
			diff += rd.Sts[i].Sub(rd.Rts[i])
			sessionCnt++
		}
	}
	ms := int64(diff / time.Millisecond)
	ms /= sessionCnt
	return ms
}

//data for one session group
func addGroupPoints(grps []uint, grpMap map[uint]groupRecord) plotter.XYs {
	pts := make(plotter.XYs, len(grps))
	for idx, grp := range grps {
		ms := getGrpLatency(grpMap[grp].grMap)
		x := float64(grp)
		y := float64(ms)
		pts[idx].X = x
		pts[idx].Y = y
	}
	return pts
}

func plotGroupsGraph(title, x, y string, grps []uint, grpMap map[uint]groupRecord) error {
	p, err := plot.New()
	if err != nil {
		log.V(1).Infof("plot %v", err)
		return err
	}

	p.Title.Text = title
	p.X.Label.Text = x
	p.Y.Label.Text = y

	err = plotutil.AddLinePoints(p,
		"Groups", addGroupPoints(grps, grpMap))
	if err != nil {
		log.V(1).Infof("plotutil.AddLinePoints %v", err)
		return err
	}
	// Save the plot to a PNG file.
	if err := p.Save(16*vg.Inch, 8*vg.Inch, "group_latency.png"); err != nil {
		log.V(1).Infof("save PNG %v", err)
		return err
	}
	return nil
}

/*
//data for one session group
func addPoints(grmap map[uint]*stats.Record) plotter.XYs {
	pts := make(plotter.XYs, len(grmap))
	for sessionNo, rd := range grmap {
		var diff time.Duration
		for i := 0; i < len(rd.Sts); i++ {
			diff += rd.Sts[i].Sub(rd.Rts[i])
		}
		ms := int64(diff / time.Millisecond)
		ms /= int64(len(rd.Sts))

		x := float64(sessionNo)
		y := float64(ms)
		pts[sessionNo-1].X = x
		pts[sessionNo-1].Y = y
	}
	return pts
}

// plotGraph:
// <1> if only one session group and one session, generate latency data for each poll
// <2> if only one session group but multiple session, data not so interesting.
// <3> if number of session group is large than 1, generate group latency update.
func plotGraph(title, x, y string, grpMap map[uint]groupRecord) error {
	p, err := plot.New()
	if err != nil {
		log.V(1).Infof("plot %v", err)
		return err
	}

	p.Title.Text = title
	p.X.Label.Text = x
	p.Y.Label.Text = y

	var styleSet int
	for grp, grec := range grpMap {
		name := "group" + strconv.FormatUint(uint64(grp), 10)

		linePointsData := addPoints(grec.grMap)

		lpLine, lpPoints, err := plotter.NewLinePoints(linePointsData)
		if err != nil {
			return err
		}
		lpLine.Color = plotutil.Color(styleSet)
		lpLine.Dashes = plotutil.Dashes(styleSet)
		lpPoints.Color = plotutil.Color(styleSet)
		lpPoints.Shape = plotutil.Shape(styleSet)
		styleSet++

		p.Add(lpLine, lpPoints)
		p.Legend.Add(name, lpLine, lpPoints)
	}
	// Save the plot to a PNG file.
	if err := p.Save(16*vg.Inch, 8*vg.Inch, "latency.png"); err != nil {
		log.V(1).Infof("save PNG %v", err)
		return err
	}
	return nil
}
*/
