package main

import (
    "fmt"
    "strings"
    "net"
    "time"
)

type NetworkInterfaceMap map[string]NetworkInterface
type ServiceMap map[string]Service

type SystemInfo struct {
    Interfaces NetworkInterfaceMap
    Services ServiceMap
    WifiCountryCode string
}

type MonitorReport struct {
    Full bool
    Interfaces []NetworkInterface
    Services []Service
    WifiCountryCode string
}

func (m NetworkInterfaceMap) Keys() *StringSet {
    ss := NewStringSet()
	  for k := range m {
	      ss.Add(k)
	  }
    return ss
}

func (m ServiceMap) Keys() *StringSet {
    ss := NewStringSet()
	  for k := range m {
	      ss.Add(k)
	  }
    return ss
}

func (m NetworkInterfaceMap) Values() []NetworkInterface {
    i, ns := 0, make([]NetworkInterface, len(m))
    for _,n := range m {
        ns[i] = n
        i++
    }
    return ns
}

func (m ServiceMap) Values() []Service {
    i, ss := 0, make([]Service, len(m))
    for _,s := range m {
        ss[i] = s
        i++
    }
    return ss
}

func gatherInterfaces() NetworkInterfaceMap {
    wlan00, err := DefaultWlanInterface()
    if err != nil {
        LogDebug("Cannot obtain default wlan:", err)
    }

    isDefaultWlan := func (n string) bool {
        return n == wlan00
    }

    ifmap := make(NetworkInterfaceMap)
    ifaces, err := net.Interfaces()
    if err != nil {
        LogDebug("Cannot obtain network interfaces:", err)
        return ifmap
    }

    for _,i := range ifaces {
        if i.Name == "lo" { continue }

        ps := NewStringSet()
        addrs, err := i.Addrs()
        if err != nil {
            ifmap[i.Name] = NetworkInterface{
                                    Name: i.Name,
                                    IPs: ps,
                                    WiFi: isDefaultWlan(i.Name)}
            LogDebugf("Cannot obtain addresses for %v: %v", i.Name, err)
            continue
        }

        for _,a := range addrs {
            switch b := a.(type) {
            case *net.IPNet:
                ps.Add(b.IP.String())
            case *net.IPAddr:
                ps.Add(b.IP.String())
            }
        }

        if strings.HasPrefix(i.Name, "wlan") && ps.Size() > 0 {
            ssid, err := ReportSsid(i.Name)
            if err != nil {
                LogDebug("Cannot obtain SSID:", err)
            }

            ifmap[i.Name] = NetworkInterface{
                                    Name: i.Name,
                                    IPs: ps,
                                    SSID: ssid,
                                    WiFi: isDefaultWlan(i.Name)}
        } else {
            ifmap[i.Name] = NetworkInterface{
                                    Name: i.Name,
                                    IPs: ps,
                                    WiFi: isDefaultWlan(i.Name)}
        }
    }
    return ifmap
}

func gatherServices() ServiceMap {
    ssh,_ := ServiceIsRunning("SSH")
    vnc,_ := ServiceIsRunning("VNC")
    return ServiceMap{
        "SSH": Service{"SSH", ssh},
        "VNC": Service{"VNC", vnc},
    }
}

func getWifiCountryCode() string {
    c,_ := WifiCountryCode()
    return c
}

func inspectSystem() *SystemInfo {
    return &SystemInfo { gatherInterfaces(), gatherServices(), getWifiCountryCode() }
}

func produceFullReport(s *SystemInfo) *MonitorReport {
    return &MonitorReport { true, s.Interfaces.Values(), s.Services.Values(), s.WifiCountryCode }
}

func produceReport(new *SystemInfo, old *SystemInfo) *MonitorReport {
    if !new.Interfaces.Keys().Equal(old.Interfaces.Keys()) ||
            !new.Services.Keys().Equal(old.Services.Keys()) {
        return produceFullReport(new)
    }

    var ifaces []NetworkInterface
    for name, f := range new.Interfaces {
        if !f.Equal(old.Interfaces[name]) {
            ifaces = append(ifaces, f)
        }
    }

    var servs []Service
    for name, s := range new.Services {
        if s != old.Services[name] {
            servs = append(servs, s)
        }
    }

    if ifaces == nil && servs == nil && new.WifiCountryCode == old.WifiCountryCode {
        return nil
    } else {
        return &MonitorReport { false, ifaces, servs, new.WifiCountryCode }
    }
}

const (
    MonitorStart = 1 << iota
    MonitorBurst
    MonitorStop
)

func MonitorSystem(in <-chan int, out chan<- *MonitorReport, notify chan<- int, id int) {
    defer RecoverDo(
        func(x interface{}) {
            notify <- id
            LogDebug("Monitor terminates due to:", x)
        },
        func() {
            LogDebug("Monitor terminates normally")
        },
    )

    var current *SystemInfo

    // Wait for first control code ...
    ctrl, ok := <-in
    if !ok {
        panic("First control code never arrives")
    }

    // ... which must be MonitorStart
    switch ctrl {
    case MonitorStart:
        s := inspectSystem()
        r := produceFullReport(s)

        current = s
        out <- r
    default:
        panic(fmt.Sprintf("Invalid first control code: %v", ctrl))
    }

    // Regular report interval = 3 sec
    regularTicker := time.NewTicker(3 * time.Second)
    defer regularTicker.Stop()
    active := true

    // Report more frequently on receiving MonitorBurst
    burstTicker := time.NewTicker(1200 * time.Millisecond)
    defer burstTicker.Stop()
    bursts := 0

    for {
        select {
        case ctrl, ok = <-in:
            if !ok { return }

            switch ctrl {
            case MonitorStart:
                active = true
                s := inspectSystem()
                r := produceFullReport(s)

                current = s
                out <- r

            case MonitorBurst:
                bursts = 9

            case MonitorStop:
                bursts = 0
                active = false

            default:
                panic(fmt.Sprintf("Invalid monitor control code: %v", ctrl))
            }
        case <-regularTicker.C:
            if active {
                s := inspectSystem()
                r := produceReport(s, current)

                current = s
                out <- r
            }

        case <-burstTicker.C:
            if active && bursts > 0 {
                bursts--
                s := inspectSystem()
                r := produceReport(s, current)

                current = s
                out <- r
            }
        }
    }
}
