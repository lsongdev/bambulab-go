# 局域网发现与 `snapshot`

Bambu 云端的 `GET /v1/iot-service/api/user/bind` 返回已绑定打印机的
`dev_id`（序列号）和 `dev_access_code`，当前 SDK 的 `Device` 结构中没有 IP。
Bambu Studio 的网络代理公开了 `start_discovery` 和 SSDP 消息回调；
[其源码接口](https://github.com/bambulab/BambuStudio/blob/master/src/slic3r/Utils/NetworkAgent.hpp)
能看到这些调用。
[独立的网络协议抓包记录](https://github.com/ClusterM/open-bamboo-networking/blob/master/research/06.01-ssdp.md)
说明打印机会向 UDP 2021 发送 `NOTIFY` 广播，`USN` 是序列号，
`Location` 是局域网 IP。

2026-09-23 在当前用户的 A1 mini 所在网络也收到了同样的广播：
源地址为打印机的局域网 IP，`USN` 与云端绑定记录的序列号一致；
复测时这台 A1 mini 两次广播相隔约 10.2 秒，因此默认发现窗口设为 15 秒；
实际报文的 `HOST` 头是 `239.255.255.250:1900`。不同固件的
`HOST` 头值可能不同；真正接收广播的 UDP 端口是 2021。

`bambulab snapshot` 的流程：

1. 优先使用 `--device` / `BAMBU_DEVICE_ID`；没有指定时选云端绑定列表的第一台。
2. 优先使用 `--access-code` / `BAMBU_ACCESS_CODE`；没有指定时从绑定列表取该设备的 `dev_access_code`。
3. 优先使用 `--host` / `BAMBU_HOST`；没有指定时监听 UDP 2021，等待与所选序列号匹配的 `NOTIFY`。等待上限 15 秒；发现失败提示用户提供 `--host IP`。
4. 用发现报文的**源 IP** 连接本地摄像头端口 6000。TLS 继续检查内置 CA 签名与证书中的打印机序列号，因此广播本身不作为身份凭据。
5. 读取一帧 JPEG 并保存。省略文件名时使用 `snapshot-YYYYMMDD-HHMMSS.jpg`；默认输出简短文字，`--json` 才输出 JSON。

发现只在同一局域网且广播能到达本机时有效。跨 VLAN、VPN、路由器隔离、
打印机离线或端口被阻止时，显式 `--host IP` 仍可使用。
云端没有在当前绑定设备结构中提供可直接使用的局域网 IP；
公网访问也不能用这个本地发现流程代替云端视频通道。

## 与 `ssdp-go` 的关系

[`lsongdev/ssdp-go`](https://github.com/lsongdev/ssdp-go) 原有的 `Search` 使用
`M-SEARCH` 主动查询。新版以 `Search(ctx, target)` 提供可取消的主动查询，
同时提供 `ListenNotifications(ctx, visit)` 以处理
`NOTIFY`，并将报文头解析与设备特定字段解释分开。Bambu SDK 使用这个通用
监听器，按 `NT` 和 `USN` 识别打印机，再用 UDP 源 IP 作为候选地址。
[Yeelight 的官方协议](https://www.yeelight.com/download/Yeelight_Inter-Operation_Spec.pdf)
也支持主动查询和周期通知；`yeelight-go` 已改用带 context 的 `Search`。

与 UPnP/SSDP 标准相比，Yeelight 把多播端口改为 1982、使用自定义的
`ST: wifi_bulb` 和 `yeelight://` 地址；其通知示例没有标准设备类型与
`USN`。Bambu 把数据报发到 UDP 2021 的广播地址，实际设备报文的
`HOST` 头却仍是 `239.255.255.250:1900`，`Location` 只是裸 IP，
不是设备描述 URL；当前抓包没有 `NTS: ssdp:alive`。这些差异让通用库
负责收发与解析，具体协议检查留给设备 SDK。

`bambulab-go` 和 `yeelight-go` 都固定到相同的 `ssdp-go` 提交；
通用库改动已通过 [ssdp-go PR #1](https://github.com/lsongdev/ssdp-go/pull/1) 合并。

## SDK 使用

```go
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
printer, err := bambulab.DiscoverPrinter(ctx, "PRINTER_SERIAL")
if err != nil { /* handle discovery failure */ }
fmt.Println(printer.ID, printer.IP, printer.Name, printer.Model)

// Collect all printers heard during a bounded window:
printers, err := bambulab.DiscoverPrinters(ctx, 15*time.Second)
```

这两个函数在调用时临时监听 UDP 2021，成功或超时后关闭 socket。
它们不启动常驻服务，也不在后台维护 IP 缓存。云端绑定信息提供身份和
Access Code，局域网广播只提供临时地址；两者按序列号关联。

2026-09-23 的 Yeelight 实测：`Search(ctx, "wifi_bulb")` 找到一盏灯，
响应 `Location` 为 `yeelight://192.168.8.182:55443`，并返回
`Cache-Control: max-age=3600`；同期监听 25 秒未收到灯泡 `NOTIFY`。
通知可能较稀疏，短窗口内未观察到不能证明设备不发送通知。
