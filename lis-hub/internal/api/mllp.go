package api

import (
	"bufio"
	"log"
	"net"
	"time"
)

// MLLP/TCP 监听器: 自动识别 MLLP(HL7, VT...FS CR) 和 ASTM(STX 帧)
func (s *Server) ListenTCP(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	log.Printf("接口接入监听: %s (MLLP/HL7 + ASTM 自动识别)", addr)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handleConn(conn)
		}
	}()
	return nil
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	remote := conn.RemoteAddr().String()
	var buf []byte
	tmp := make([]byte, 4096)
	br := bufio.NewReader(conn)

	// MLLP: 0x0B ... 0x1C 0x0D ; ASTM: STX 帧, 以 ETX+checksum+CRLF 结束
	inMLLP := false
	for {
		n, err := br.Read(tmp)
		if n > 0 {
			chunk := tmp[:n]
			for _, b := range chunk {
				if b == 0x0B && !inMLLP && len(buf) == 0 {
					inMLLP = true
					continue
				}
				buf = append(buf, b)
				if inMLLP && len(buf) >= 2 && buf[len(buf)-2] == 0x1C && buf[len(buf)-1] == 0x0D {
					msg := buf[:len(buf)-2]
					buf = nil
					inMLLP = false
					s.ingestRemote(msg, remote, "hl7")
					_, _ = conn.Write([]byte{0x06}) // ACK(简化)
				}
			}
			if !inMLLP && len(buf) > 0 {
				// ASTM: 检查是否收到完整帧序列(以 L|1 终止记录或超时)
				if looksCompleteASTM(buf) {
					s.ingestRemote(buf, remote, "astm")
					buf = nil
					_, _ = conn.Write([]byte{0x06})
				}
			}
		}
		if err != nil {
			// 超时/断开: 残余数据按 ASTM 容错处理(截断报文也能抢救)
			if len(buf) > 0 && !inMLLP {
				s.ingestRemote(buf, remote, "astm")
			}
			return
		}
	}
}

func looksCompleteASTM(buf []byte) bool {
	// 含 L| 终止记录且帧尾完整
	for i := 0; i+3 < len(buf); i++ {
		if buf[i] == 'L' && buf[i+1] == '|' {
			// 找后续 ETX
			for j := i; j < len(buf); j++ {
				if buf[j] == 0x03 {
					return true
				}
			}
		}
	}
	return false
}

func (s *Server) ingestRemote(msg []byte, remote, protocol string) {
	rawID, outcomes, err := s.en.Ingest(msg, "", protocol) // 来源以报文内标识为准(MSH-3/H记录)
	if err != nil {
		log.Printf("接入失败(%s): %v", remote, err)
		return
	}
	log.Printf("接入 %s: raw#%d, %d 条结果", remote, rawID, len(outcomes))
}
