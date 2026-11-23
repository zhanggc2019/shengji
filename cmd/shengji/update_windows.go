//go:build windows && !server

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/zhanggc2019/shengji/internal/buildinfo"
	"github.com/zhanggc2019/shengji/internal/update"
)

func (session *desktopSession) watchUpdates(ctx context.Context, tray *application.SystemTray) {
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			session.checkForUpdate(ctx, tray, false)
			timer.Reset(6 * time.Hour)
		}
	}
}

func (session *desktopSession) checkForUpdate(ctx context.Context, tray *application.SystemTray, manual bool) {
	if ctx.Err() != nil || !session.updateBusy.CompareAndSwap(false, true) {
		return
	}
	defer session.updateBusy.Store(false)

	checkCtx, stop := context.WithTimeout(ctx, 15*time.Second)
	installer, err := update.Check(checkCtx, buildinfo.UpdateRepository, buildinfo.Version(), runtime.GOARCH, buildinfo.OfflineInstaller == "true")
	stop()
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		if manual {
			session.app.Dialog.Error().SetTitle("检查更新失败").SetMessage(err.Error()).Show()
		} else {
			log.Printf("检查更新失败: %v", err)
		}
		return
	}
	if installer == nil {
		if manual {
			session.app.Dialog.Info().SetTitle("声记更新").SetMessage("未发现更新。当前版本 v" + buildinfo.Version()).Show()
		}
		return
	}
	if !manual && session.lastOfferedVersion == installer.Version {
		return
	}
	session.lastOfferedVersion = installer.Version
	if !session.confirmUpdate("发现新版本", fmt.Sprintf("当前版本 v%s，已发布 v%s。", buildinfo.Version(), installer.Version), "下载安装包") {
		return
	}

	tray.SetTooltip("声记 · 正在下载更新")
	downloadCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	path, err := installer.Download(downloadCtx)
	cancel()
	if ctx.Err() != nil {
		if path != "" {
			update.Remove(path)
		}
		return
	}
	tray.SetTooltip("声记")
	if err != nil {
		session.app.Dialog.Error().SetTitle("下载更新失败").SetMessage(err.Error()).Show()
		return
	}
	if !session.confirmUpdate("安装声记 v"+installer.Version, "安装前会退出声记。正在录制的音频将保存，后台任务将在下次启动时继续。请先保存正在编辑的文字。", "退出并安装") || ctx.Err() != nil {
		update.Remove(path)
		return
	}
	// The installer waits for this process to exit before replacing the executable.
	cmd := exec.Command(path, "/SHENGJI_PID="+strconv.Itoa(os.Getpid()))
	if err = cmd.Start(); err != nil {
		update.Remove(path)
		session.app.Dialog.Error().SetTitle("启动安装程序失败").SetMessage(err.Error()).Show()
		return
	}
	_ = cmd.Process.Release()
	session.app.Quit()
}

func (session *desktopSession) confirmUpdate(title, message, action string) bool {
	confirmed := false
	dialog := session.app.Dialog.Question().SetTitle(title).SetMessage(message)
	dialog.AddButton(action).OnClick(func() { confirmed = true })
	dialog.AddButton("稍后").SetAsDefault().SetAsCancel()
	dialog.Show()
	return confirmed
}
