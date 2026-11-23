package main

import "github.com/zhanggc2019/shengji/internal/launch"

// main starts the desktop application and its shared HTTP service.
func main() {
	launch.Main(launch.Options{
		Listen: "0.0.0.0:9011",
		NewDesktop: func(root string) (launch.Desktop, error) {
			session, err := newDesktopSession(root)
			if err != nil {
				return nil, err
			}
			return session.run, nil
		},
		ShowError: startupError,
	})
}
