// Copyright 2019 go-gtp authors. All rights reserved.
// Use of this source code is governed by a MIT-style license that can be
// found in the LICENSE file.

/*
Package ies provides encoding/decoding feature of GTPv0 Information Elements.
*/
package ies

import (
	"encoding/binary"
	"fmt"
)

// TV IE definitions.
const (
	_ uint8 = iota
	Cause
	IMSI
	RouteingAreaIdentity
	TemporaryLogicalLinkIdentity
	PacketTMSI
	QualityOfServiceProfile
	_
	ReorderingRequired
	AuthenticationTriplet // 9
	_
	MAPCause // 11
	PTMSISignature
	MSValidated
	Recovery
	SelectionMode
	FlowLabelDataI
	FlowLabelSignalling
	FlowLabelDataII
	MSNotReachableReason
	ChargingID uint8 = 127
)

// TLV IE definitions.
const (
	EndUserAddress uint8 = iota + 128
	MMContext
	PDPContext
	AccessPointName
	ProtocolConfigurationOptions
	GSNAddress
	MSISDN
	ChargingGatewayAddress uint8 = 251
	PrivateExtension       uint8 = 255
)

// IE is a GTPv0 Information Element.
type IE struct {
	Type    uint8
	Length  uint16
	Payload []byte
}

// New creates new IE.
func New(t uint8, p []byte) *IE {
	i := &IE{Type: t, Payload: p}
	i.SetLength()
	return i
}

// Serialize returns the byte sequence generated from an IE instance.
func (i *IE) Serialize() ([]byte, error) {
	b := make([]byte, i.Len())
	if err := i.SerializeTo(b); err != nil {
		return nil, err
	}
	return b, nil
}

// SerializeTo puts the byte sequence in the byte array given as b.
func (i *IE) SerializeTo(b []byte) error {
	if len(b) < i.Len() {
		return ErrTooShortToSerialize
	}

	var offset = 1
	b[0] = i.Type
	if !i.IsTV() {
		binary.BigEndian.PutUint16(b[1:3], i.Length)
		offset += 2
	}
	copy(b[offset:i.Len()], i.Payload)
	return nil
}

// Decode decodes given byte sequence as a GTPv0 Information Element.
func Decode(b []byte) (*IE, error) {
	i := &IE{}
	if err := i.DecodeFromBytes(b); err != nil {
		return nil, err
	}
	return i, nil
}

// DecodeFromBytes sets the values retrieved from byte sequence in GTPv0 IE.
func (i *IE) DecodeFromBytes(b []byte) error {
	if len(b) < 2 {
		return ErrTooShortToDecode
	}

	i.Type = b[0]
	if i.IsTV() {
		return decodeTVFromBytes(i, b)
	}
	return decodeTLVFromBytes(i, b)
}

func decodeTVFromBytes(i *IE, b []byte) error {
	l := len(b)
	if l < 2 {
		return ErrTooShortToDecode
	}
	if i.Len() > l {
		return ErrInvalidLength
	}
	i.Length = 0
	i.Payload = b[1:i.Len()]

	return nil
}

func decodeTLVFromBytes(i *IE, b []byte) error {
	l := len(b)
	if l < 3 {
		return ErrTooShortToDecode
	}

	i.Length = binary.BigEndian.Uint16(b[1:3])
	if int(i.Length)+3 > l {
		return ErrInvalidLength
	}

	i.Payload = b[3 : 3+int(i.Length)]
	return nil
}

var tvLengthMap = map[uint8]int{
	0:   0,  // Reserved
	1:   1,  // Cause
	2:   8,  // IMSI
	3:   6,  // RAI
	4:   4,  // TLLI
	5:   4,  // P-TMSI
	6:   3,  // QoS
	8:   1,  // Reordering Required
	9:   28, // Authentication Triplet
	11:  1,  // MAP Cause
	12:  3,  // P-TMSI Signature
	13:  1,  // MS Validated
	14:  1,  // Recovery
	15:  1,  // Selection Mode
	16:  2,  // Flow Label Data I
	17:  2,  // Flow Label Signalling
	18:  3,  // Flow Label Data II
	19:  1,  // MS Not Reachable Reason
	127: 4,  // Charging ID
}

// IsTV checks if a IE is TV format. If false, it indicates the IE has Length inside.
func (i *IE) IsTV() bool {
	return int(i.Type) < 0x80
}

// Len returns the actual length of IE.
func (i *IE) Len() int {
	if l, ok := tvLengthMap[i.Type]; ok {
		return l + 1
	}
	if i.Type < 128 {
		return 1 + len(i.Payload)
	}
	return 3 + len(i.Payload)
}

// SetLength sets the length in Length field.
func (i *IE) SetLength() {
	if _, ok := tvLengthMap[i.Type]; ok {
		i.Length = 0
		return
	}

	i.Length = uint16(len(i.Payload))
}

// String returns the GTPv0 IE values in human readable format.
func (i *IE) String() string {
	return fmt.Sprintf("{Type: %d, Length: %d, Payload: %#v}",
		i.Type,
		i.Length,
		i.Payload,
	)
}

// DecodeMultiIEs decodes multiple (unspecified number of) IEs to []*IE at a time.
func DecodeMultiIEs(b []byte) ([]*IE, error) {
	var ies []*IE
	for {
		if len(b) == 0 {
			break
		}

		i, err := Decode(b)
		if err != nil {
			return nil, err
		}

		ies = append(ies, i)
		b = b[i.Len():]
		continue
	}
	return ies, nil
}

func newUint8ValIE(t, v uint8) *IE {
	return New(t, []byte{v})
}

func newUint16ValIE(t uint8, v uint16) *IE {
	i := New(t, make([]byte, 2))
	binary.BigEndian.PutUint16(i.Payload, v)
	return i
}

func newUint32ValIE(t uint8, v uint32) *IE {
	i := New(t, make([]byte, 4))
	binary.BigEndian.PutUint32(i.Payload, v)
	return i
}

func newStringIE(t uint8, str string) *IE {
	return New(t, []byte(str))
}
