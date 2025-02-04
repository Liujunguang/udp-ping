# UDP Ping

以 client-server 模式发送和接收 UDP 数据包，检测两个对端的网络情况，并输出探测结果。

## 用法

编译:
```shell
go build .
```

`server` 侧:
```shell
./udp_demo -s -local 0.0.0.0:10000
```

`client` 侧:
```shell
./udp_demo -host [target_ip] -port 10000
```

## 参数

- `s`: 启动 server 模式
- `host`: 目标地址
- `port`: 目标端口
- `local`: 本地 UDP 监听地址
- `proxy`: 是否开启本地代理协议转发
- `proxyAddress`: 本地代理协议转发地址
- `rate`: 发包频率(ms)，默认 1000ms
- `num`: 发送、接收的包数，默认 1000
- `max`: 随机包的大小上限，默认 1100字节
- `min`: 随机包的大小下限，默认 100字节
- `div`: 随机包大小的偏移量，默认 400字节，max=maxExp+maxDiv+rand，min=maxExp-maxDiv+rand

## 特点

- 无配置文件
- 支持多开
- 支持本地代理协议，默认支持 SOCKS5
- 支持 CRC32 校验
