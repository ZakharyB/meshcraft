package main

import (
	"fmt"
	"os"
	"time"

	"github.com/ZakharyB/meshcraft/internal/mcproto"
)

func main() {
	st, rtt, err := mcproto.PingStatus(os.Args[1], 10*time.Second)
	if err != nil { fmt.Println("PING FAIL:", err); os.Exit(1) }
	fmt.Printf("STATUS OK version=%s protocol=%d players=%d/%d rtt=%s\n", st.VersionName, st.VersionProtocol, st.PlayersOnline, st.PlayersMax, rtt)
	lr, err := mcproto.LoginProbe(os.Args[1], "MeshCraftProbe", 10*time.Second)
	if err != nil { fmt.Println("LOGIN FAIL:", err); os.Exit(1) }
	fmt.Printf("LOGIN PROBE kind=%s reason=%.80s\n", lr.Kind, lr.Reason)
}
