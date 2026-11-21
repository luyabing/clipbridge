package app

import "time"

func (a *App) startMaintenance() {
	a.dialManualPeers()
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		dialTicker := time.NewTicker(5 * time.Second)
		gcTicker := time.NewTicker(30 * time.Second)
		defer dialTicker.Stop()
		defer gcTicker.Stop()
		for {
			select {
			case <-a.ctx.Done():
				return
			case <-dialTicker.C:
				a.dialManualPeers()
			case <-gcTicker.C:
				if a.hub != nil {
					a.hub.maintenanceGC()
				}
			}
		}
	}()
}

func (a *App) dialManualPeers() {
	if a.hub == nil {
		return
	}
	cfg := a.Config()
	for _, addr := range cfg.ManualPeers {
		a.hub.DialManual(addr)
	}
}
