package main

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"math/big"
	rand2 "math/rand"
	"net"
	"time"
)

const (
	receiveTimeout = 10

	// 收包buf大小
	maxRecvBufSize = 1024 * 10
	// 大小包数比
	maxMinRatio = 3

	dataLenLength = 4
	indexLen      = 4
	crc32Len      = 4

	ipv4Address = uint8(0x01)
	//domainNameAddress = uint8(0x03)
	ipv6Address = uint8(0x04)

	lenAddrType        = int(1)
	lenIPv4            = int(4)
	lenIPv6            = int(16)
	lenPort            = int(2)
	lenAssociateHeader = int(3)
)

// CRC数据
type CRCBuffer struct {
	dataLen uint32
	index   uint32
	crc32   uint32
	buffer  []byte
}

// 接收数据
type RecvData struct {
	readDataLen int
	tRecv       int64
	data        []byte
}

var (
	server       = flag.Bool("s", false, "for demo server")
	host         = flag.String("host", "127.0.0.1", "target host addr")
	port         = flag.String("port", "6062", "target port")
	local        = flag.String("local", "0.0.0.0:0", "local listening address")
	proxyFlag    = flag.String("proxy", "off", "proxy status")
	proxyAddress = flag.String("ph", "127.0.0.1:10080", "proxy address")
	rate         = flag.Int("rate", 1000, "interval of each sending")
	num          = flag.Int("n", 1000, "numbers of packets for sending/receiving")
	maxSizeExp   = flag.Int("max", 1100, "expectation of big packet")
	minSizeExp   = flag.Int("min", 100, "expectation of small packet")
	maxSizeDiv   = flag.Int("div", 400, "deviation of big packet expectation")

	// 真随机
	rSeed, _     = rand.Int(rand.Reader, big.NewInt(math.MaxInt64))
	randomObject = rand2.New(rand2.NewSource(rSeed.Int64()))
)

var isProxy bool
var proxyAddr *net.UDPAddr

func main() {
	flag.Parse()

	if *maxSizeExp < *minSizeExp || *maxSizeExp < *maxSizeDiv || *maxSizeExp+*maxSizeDiv > maxRecvBufSize {
		fmt.Println("invalid expectation or deviation of packet size, max: ", *maxSizeExp, " min: ", *minSizeExp, " maxDiv: ", *maxSizeDiv)
		return
	}

	if *proxyFlag == "on" || *proxyFlag == "ON" {
		isProxy = true
	}

	if isProxy {
		udpAddr, err := net.ResolveUDPAddr("udp", *proxyAddress)
		if err != nil {
			fmt.Println("server command udp addr err: ", err)
			return
		}
		proxyAddr = udpAddr
	}

	addr := net.JoinHostPort(*host, *port)
	udpServerAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		fmt.Println("resolve udp addr err: ", err)
	}

	udpAddr, err := net.ResolveUDPAddr("udp", *local)
	if err != nil {
		fmt.Println("resolve udp addr err: ", err)
		return
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		fmt.Println("listen ", udpAddr, " failed, err: ", err)
		return
	}
	fmt.Println("listening on: ", conn.LocalAddr())
	defer func() {
		_ = conn.Close()
	}()

	// 收包
	interT := time.NewTimer(receiveTimeout * time.Second)
	defer interT.Stop()

	var countRcv, singleRecv int
	ch := make(chan struct{})
	sCh := make(chan *net.UDPAddr)
	var remote *net.UDPAddr
	received := false
	receiveData := make([]*RecvData, 0, *num)

	var startRTime time.Time
	var endRTime time.Time
	go func() {
		for {
			if countRcv >= *num {
				ch <- struct{}{}
				break
			}

			buf := make([]byte, maxRecvBufSize)
			singleRecv, remote, err = readFromUDPWidthProxy(conn, buf)
			if err != nil {
				fmt.Println("read data from udp failed, err: ", err)
				ch <- struct{}{}
				break
			}

			if *server && !received {
				sCh <- remote
			}

			// 从第一次收到数据开始计时
			if !received {
				startRTime = time.Now()
				received = true
			}

			countRcv++
			if !interT.Stop() {
				<-interT.C
			}
			interT.Reset(receiveTimeout * time.Second)

			temp := &RecvData{
				readDataLen: singleRecv,
				tRecv:       time.Now().UnixNano(),
				data:        buf,
			}
			// 收到的数据暂存
			receiveData = append(receiveData, temp)
			fmt.Println("receive data count: ", countRcv, " len: ", singleRecv)
		}
	}()

	var countSend, nSend int
	rateT := time.NewTimer(0)
	defer rateT.Stop()

	sendBuffs, maxSendPkCount, maxSPkLen, minSPkCount, minSPkLen := newSendBuffer(*maxSizeExp, *minSizeExp, *maxSizeDiv, *num, maxMinRatio)

	if *server {
		udpServerAddr = <-sCh
	}

	startSTime := time.Now()
	var endSTime time.Time
L:
	for _, sendBuf := range sendBuffs {
		select {
		case <-rateT.C:
			tS := time.Now().UnixNano()
			tsByte := make([]byte, 8)
			binary.BigEndian.PutUint64(tsByte, uint64(tS))
			sendBuf.buffer = append(tsByte, sendBuf.buffer...)

			n, err := sendUDPWidthProxy(conn, udpServerAddr, sendBuf.buffer)
			if err != nil {
				fmt.Println("send udp width proxy failed, err: ", err)
				break L
			}
			nSend += n
			rateT.Reset(time.Duration(*rate) * time.Microsecond)
			countSend++
			fmt.Println("send data count: ", countSend, " size: ", n)
		}
	}
	endSTime = time.Now()
	fmt.Println("数据已发送完")

	select {
	case <-interT.C:
		// 超时退出
		endRTime = time.Now().Add(-receiveTimeout * time.Second)
		fmt.Println("接收超时！")
	case <-ch:
		endRTime = time.Now()
		fmt.Println("已收到设定包数！")
	}

	fmt.Println("正在分析数据...")
	var sumRecvLen, stickyCount, invalidCount, validCount, maxRCount, minRCount int
	var validLen, maxRLen uint32
	var minRLen = uint32(99999999)
	var minT = int64(99999999)
	var maxT, sumT int64
	for _, d := range receiveData {
		sumRecvLen += d.readDataLen
		tSend := int64(binary.BigEndian.Uint64(d.data[:8]))                                                  // 0-8 时间
		dataL := binary.BigEndian.Uint32(d.data[8 : 8+dataLenLength])                                        // 8-12 长度
		index := binary.BigEndian.Uint32(d.data[8+dataLenLength : 8+dataLenLength+indexLen])                 // 12-16 编号
		crc := binary.BigEndian.Uint32(d.data[8+dataLenLength+indexLen : 8+dataLenLength+indexLen+crc32Len]) // 16-20 crc32
		// 包中数据长不含编号和crc
		if uint32(d.readDataLen) > dataL+8+dataLenLength+indexLen+crc32Len {
			stickyCount++
			fmt.Println("发生黏包，包编号: ", index, " data len: ", dataL, " recv len: ", d.readDataLen)
			continue
		}
		buf := d.data[8+dataLenLength+indexLen+crc32Len : 8+dataLenLength+indexLen+crc32Len+dataL]
		// crc32校验
		if crc32Verify(buf) != crc {
			invalidCount++
			fmt.Println("包校验失败，包编号: ", index, " data len: ", dataL)
			continue
		}
		validCount++
		validLen += dataL
		tt := (d.tRecv - tSend) / 1e6
		sumT += tt
		if minT > tt {
			minT = tt
		}
		if maxT < tt {
			maxT = tt
		}
		if dataL > uint32(*minSizeExp) {
			maxRCount++
			if maxRLen < dataL {
				maxRLen = dataL
			}
			continue
		}
		minRCount++
		if minRLen > dataL {
			minRLen = dataL
		}
	}

	fmt.Println("统计结果:")
	fmt.Printf(""+
		"\t设定收发包数: %v\n"+
		"\t发送包个数: %v\n"+
		"\t发送总字节数: %v bit\n"+
		"\t接收次数: %v\n"+
		"\t接收总字节数: %v bit\n"+
		"\t黏包数: %v\n"+
		"\tcrc校验无效包数: %v\n"+
		"\t接收有效包数: %v\n"+
		"\t接收有效数据总字节数: %v bit\n"+
		"\t丢包个数: %v\n"+
		"\t丢包率: %v %%\n"+
		"\t发送总时长: %v\n"+
		"\t接收总时长: %v\n"+
		"\t发送码率: %v Mbit/s\n"+
		"\t接收码率: %v Mbit/s\n"+
		"\t平均时延: %v ms\n"+
		"\t最小时延: %v ms\n"+
		"\t最大时延: %v ms\n"+
		"\t发送大包数: %v\n"+
		"\t发送最大包字节数: %v bit\n"+
		"\t发送小包数: %v\n"+
		"\t发送最小包字节数: %v\n"+
		"\t接收大包数: %v\n"+
		"\t接收最大包字节数: %v bit\n"+
		"\t接收小包数: %v\n"+
		"\t接收最小包字节数: %v bit\n",
		*num,            // 设定收发包数
		countSend,       // 发送包个数
		nSend,           // 发送总字节数
		countRcv,        // 接收次数
		sumRecvLen,      // 接收总字节数
		stickyCount,     // 黏包数
		invalidCount,    // crc校验无效包数
		validCount,      // 接收有效包数
		validLen,        // 接收有效数据总字节数
		*num-validCount, // 丢包个数
		float64(*num-validCount)*100/float64(*num),                           // 丢包率
		endSTime.Sub(startSTime),                                             // 发送总时长
		endRTime.Sub(startRTime),                                             // 接收总时长
		float64(nSend)*8/endSTime.Sub(startSTime).Seconds()/(1024*1024),      // 发送码率
		float64(sumRecvLen)*8/endRTime.Sub(startRTime).Seconds()/(1024*1024), // 接收码率
		float64(sumT)/float64(validCount),                                    // 平均时延
		minT,                                                                 // 最小时延
		maxT,                                                                 // 最大时延
		maxSendPkCount,                                                       // 发送大包数
		maxSPkLen,                                                            // 发送最大包字节数
		minSPkCount,                                                          // 发送小包数
		minSPkLen,                                                            // 发送最小包字节数
		maxRCount,                                                            // 接收大包数
		maxRLen,                                                              // 接收最大包字节数
		minRCount,                                                            // 接收小包数
		minRLen,                                                              // 接收最小包字节数
	)
	fmt.Println("end")
	fmt.Println("remote addr: ", udpServerAddr)
}

func sendUDPWidthProxy(conn *net.UDPConn, addr *net.UDPAddr, buf []byte) (int, error) {
	if isProxy {
		pLen := len(buf)

		addrHead, err := socksMakeHeader(addr)
		if err != nil {
			return 0, err
		}
		addrLen := len(addrHead)

		headerLen := 3 + addrLen
		totalLen := headerLen + pLen
		newBuf := make([]byte, 0, totalLen)
		// 加代理头数据
		newBuf = append(newBuf, []byte{0x00, 0x00, 0x00}...)
		newBuf = append(newBuf, addrHead...)
		// 加用户数据
		newBuf = append(newBuf, buf...)

		count, err := conn.WriteToUDP(newBuf, proxyAddr)
		if err != nil {
			return 0, err
		}
		if count < totalLen {
			return count - headerLen, nil
		}
		return pLen, nil
	}
	return conn.WriteToUDP(buf, addr)
}

func readFromUDPWidthProxy(udpConn *net.UDPConn, buf []byte) (int, *net.UDPAddr, error) {
	num, remoteAddr, err := udpConn.ReadFromUDP(buf)
	if err != nil {
		return 0, nil, err
	}

	if isProxy {
		if remoteAddr.String() != proxyAddr.String() {
			err = errors.New("remote addr is not proxy addr")
			return 0, nil, err
		}
		// 从代理地址来的数据
		// 获取实际地址和地址长度
		addrLen, _, err := socksGetAddrFromHeader(buf[lenAssociateHeader:])
		if err != nil {
			return 0, nil, err
		}
		socksHeaderLen := lenAssociateHeader + addrLen
		num = num - socksHeaderLen
		// 移动数据，删除socks5头
		copy(buf[0:num], buf[socksHeaderLen:socksHeaderLen+num])
	}
	return num, remoteAddr, nil
}

func socksMakeHeader(addr *net.UDPAddr) ([]byte, error) {

	var addrType uint8
	var addrBody []byte
	var addrPort uint16
	switch {
	case addr == nil:
		addrType = ipv4Address
		addrBody = []byte{0, 0, 0, 0}
		addrPort = 0
	case addr.IP.To4() != nil:
		addrType = ipv4Address
		addrBody = []byte(addr.IP.To4())
		addrPort = uint16(addr.Port)
	case addr.IP.To16() != nil:
		addrType = ipv6Address
		addrBody = []byte(addr.IP.To16())
		addrPort = uint16(addr.Port)
	default:
		return nil, fmt.Errorf("failed to make socks5 header: %v", addr)
	}
	ret := make([]byte, len(addrBody)+3)
	ret[0] = addrType
	copy(ret[1:], addrBody)
	ret[1+len(addrBody)] = byte(addrPort >> 8)
	ret[1+len(addrBody)+1] = byte(addrPort & 0xff)
	return ret, nil
}

func socksGetAddrFromHeader(buf []byte) (addrLen int, addr *net.UDPAddr, err error) {
	ipLen := 0

	// Get the address type
	addrType := make([]byte, lenAddrType)
	copy(addrType, buf[0:lenAddrType])
	// Handle on a per type basis
	switch addrType[0] {
	case ipv4Address:
		ipLen = int(lenIPv4)
	case ipv6Address:
		ipLen = int(lenIPv6)
	default:
		return 0, nil, errors.New("ip type error")
	}
	ip := make([]byte, ipLen)
	copy(ip, buf[lenAddrType:lenAddrType+ipLen])

	port := make([]byte, lenPort)
	copy(port, buf[lenAddrType+ipLen:lenAddrType+ipLen+lenPort])

	Port := (int(port[0]) << 8) | int(port[1])
	addr = &net.UDPAddr{IP: ip, Port: Port}

	return lenAddrType + ipLen + lenPort, addr, nil
}

// 给定buffer列表，返回crc32校验值
func crc32Verify(buffer []byte) uint32 {
	ieee := crc32.NewIEEE()
	_, err := io.WriteString(ieee, string(buffer))
	if err != nil {
		panic(err)
	}
	return ieee.Sum32()
}

// 给定最大包size期望、最小包size期望、最大包size偏差、包总数、大小包数比等参数，返回待发送数据包列表
func newSendBuffer(maxExp, minExp, maxDiv, pcNum, ratio int) ([]*CRCBuffer, int, uint32, int, uint32) {
	ret := make([]*CRCBuffer, 0)
	var maxCount, minCount int
	var maxLen uint32
	var minLen = uint32(99999999)
	for i := 0; i < pcNum; i++ {
		crcBuf := &CRCBuffer{}
		// 小包，包大小偏差不大，定时发送，因此在列表中位置固定，如非定时发送，这里可将最终列表打乱，从新排列index即可
		if i%ratio == 0 {
			crcBuf = packingCRCBuffer(newBuffer(minExp), uint32(i))
			ret = append(ret, crcBuf)
			minCount++
			if minLen > crcBuf.dataLen {
				minLen = crcBuf.dataLen
			}
			continue
		}
		// 大包
		// max=maxExp+maxDiv
		// min=maxExp-maxDiv
		maxBufLen := maxExp - maxDiv + randomObject.Intn(maxDiv*2)
		crcBuf = packingCRCBuffer(newBuffer(maxBufLen), uint32(i))
		ret = append(ret, crcBuf)
		maxCount++
		if maxLen < crcBuf.dataLen {
			maxLen = crcBuf.dataLen
		}
	}

	return ret, maxCount, maxLen, minCount, minLen
}

func packingCRCBuffer(buffer []byte, index uint32) *CRCBuffer {
	crcBuf := &CRCBuffer{}
	crcBuf.dataLen = uint32(len(buffer))
	crcBuf.index = index
	crcBuf.crc32 = crc32Verify(buffer)
	dataLenByte := make([]byte, dataLenLength)
	binary.BigEndian.PutUint32(dataLenByte, crcBuf.dataLen)
	indexByte := make([]byte, indexLen)
	binary.BigEndian.PutUint32(indexByte, crcBuf.index)
	crc32Byte := make([]byte, crc32Len)
	binary.BigEndian.PutUint32(crc32Byte, crcBuf.crc32)
	crcBuf.buffer = append(crcBuf.buffer, dataLenByte...)
	crcBuf.buffer = append(crcBuf.buffer, indexByte...)
	crcBuf.buffer = append(crcBuf.buffer, crc32Byte...)
	crcBuf.buffer = append(crcBuf.buffer, buffer...)
	return crcBuf
}

// 给定长度，生成随机值列表
func newBuffer(length int) []byte {
	if length == 0 {
		return nil
	}
	var chars = []byte("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*()_+`~1234567890-====")
	cLen := len(chars)
	if cLen < 2 || cLen > 256 {
		panic("Wrong charset length for NewLenChars()")
	}
	maxRB := 255 - (256 % cLen)
	b := make([]byte, length)
	r := make([]byte, length+(length/4)) // storage for random bytes.
	i := 0
	for {
		if _, err := rand.Read(r); err != nil {
			panic("Error reading random bytes: " + err.Error())
		}
		for _, rb := range r {
			c := int(rb)
			if c > maxRB {
				continue // Skip this number to avoid modulo bias.
			}
			b[i] = chars[c%cLen]
			i++
			if i == length {
				return b
			}
		}
	}
}
