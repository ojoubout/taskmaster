package taskmaster

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func onSIGHUP(cfg *Config) {
	fmt.Println("fwefwefwefew", cfg)
	new_cfg, err := LoadConfig("taskmaster.conf")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error re-loading config:", err)
		return
	}
	*cfg = *new_cfg
}

func onSIGINT() {
	fmt.Println("\b\bGraceful shutdown...")
	os.Exit(0)
}

func SigNotifier(cfg *Config) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGINT)
	go func() {
		for {
			sig := <-sigs
			switch sig {
			case syscall.SIGHUP:
				onSIGHUP(cfg)
			case syscall.SIGINT:
				onSIGINT()
			}
		}
	}()
}
