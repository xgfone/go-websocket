// Copyright 2023 xgfone
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package websocket

import (
	"encoding/binary"
	"fmt"
)

// maskFrame masks a frame.
func maskFrame(frame, mask []byte) []byte {
	return unmaskFrame(frame, mask)
}

// unmaskFrame unmasks a frame.
func unmaskFrame(frame, mask []byte) []byte {
	if len(mask) != 4 {
		panic(fmt.Errorf("the length of frame mask is not 4"))
	}
	for i := range frame {
		frame[i] ^= mask[i%4]
	}
	return frame
}

// encodeHybi encodes a HyBi style WebSocket frame.
//
// Optional opcode:
//
//	0x0 - continuation
//	0x1 - text frame
//	0x2 - binary frame
//	0x8 - connection close
//	0x9 - ping
//	0xA - pong
func encodeHyBi(opcode int, buf, mask []byte, fin bool) []byte {
	b1 := opcode & 0x0f
	if fin {
		b1 |= 0x80
	}

	maskBit := 0
	if len(mask) > 0 {
		maskBit = 0x80
		maskFrame(buf, mask)
	}

	var header [16]byte
	var headerLen int
	payloadLen := len(buf)
	if payloadLen <= 125 {
		headerLen = 2
		header[0] = byte(b1)
		header[1] = byte(payloadLen | maskBit)
	} else if payloadLen < 65536 {
		headerLen = 4
		header[0] = byte(b1)
		header[1] = byte(126 | maskBit)
		binary.BigEndian.PutUint16(header[2:], uint16(payloadLen))
	} else {
		headerLen = 10
		header[0] = byte(b1)
		header[1] = byte(127 | maskBit)
		binary.BigEndian.PutUint64(header[2:], uint64(payloadLen))
	}
	headers := header[:headerLen]

	bs := make([]byte, 0, len(headers)+len(mask)+len(buf))
	bs = append(bs, headers...)
	bs = append(bs, mask...)
	bs = append(bs, buf...)
	return bs
}

// decodeHyBi decodes HyBi style WebSocket packets.
func decodeHyBi(frame *frame, buf []byte) bool {
	blen := len(buf)
	hlen := 2
	if blen < hlen {
		return false
	}

	b1 := int(buf[0])
	b2 := int(buf[1])

	frame.opcode = b1 & 0x0f
	frame.fin = b1&0x80 > 0
	frame.masked = b2&0x80 > 0

	if frame.masked {
		hlen += 4
		if blen < hlen {
			return false
		}
	}

	length := b2 & 0x7f
	if length == 126 {
		hlen += 2
		if blen < hlen {
			return false
		}
		length = int(binary.BigEndian.Uint16(buf[2:4]))
	} else if length == 127 {
		hlen += 8
		if blen < hlen {
			return false
		}
		length = int(binary.BigEndian.Uint64(buf[2:10]))
	}
	frame.length = hlen + length

	if blen < frame.length {
		return false
	}

	if frame.masked {
		// unmask payload
		frame.payload = unmaskFrame(buf[hlen:frame.length], buf[hlen-4:hlen])
	} else {
		frame.payload = buf[hlen:frame.length]
	}
	return true
}

type frame struct {
	opcode  int
	length  int
	payload []byte
	masked  bool
	fin     bool
}
