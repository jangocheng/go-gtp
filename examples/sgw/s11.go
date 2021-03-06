// Copyright 2019 go-gtp authors. All rights reserved.
// Use of this source code is governed by a MIT-style license that can be
// found in the LICENSE file.

// Command sgw is a dead simple implementation of S-GW only with GTP-related features.
package main

import (
	"fmt"
	"net"
	"time"

	"github.com/pkg/errors"
	v2 "github.com/wmnsk/go-gtp/v2"
	"github.com/wmnsk/go-gtp/v2/ies"
	"github.com/wmnsk/go-gtp/v2/messages"
)

func handleCreateSessionRequest(s11Conn *v2.Conn, mmeAddr net.Addr, msg messages.Message) error {
	sgw.loggerCh <- fmt.Sprintf("Received %s from %s", msg.MessageTypeName(), mmeAddr)

	s11Session := v2.NewSession(mmeAddr, &v2.Subscriber{Location: &v2.Location{}})
	s11Bearer := s11Session.GetDefaultBearer()

	// assert type to refer to the struct field specific to the message.
	// in general, no need to check if it can be type-asserted, as long as the MessageType is
	// specified correctly in AddHandler().
	csReqFromMME := msg.(*messages.CreateSessionRequest)

	var pgwAddrString string
	if ie := csReqFromMME.PGWS5S8FTEIDC; ie != nil {
		pgwAddrString = ie.IPAddress() + ":2123"
		s11Session.AddTEID(v2.IFTypeS5S8PGWGTPC, ie.TEID())
	} else {
		return &v2.ErrRequiredIEMissing{Type: ies.FullyQualifiedTEID}
	}
	if ie := csReqFromMME.SenderFTEIDC; ie != nil {
		s11Session.AddTEID(v2.IFTypeS11MMEGTPC, ie.TEID())
	} else {
		return &v2.ErrRequiredIEMissing{Type: ies.FullyQualifiedTEID}
	}

	laddr, err := net.ResolveUDPAddr("udp", *s5c)
	if err != nil {
		return err
	}
	raddr, err := net.ResolveUDPAddr("udp", pgwAddrString)
	if err != nil {
		return err
	}

	// keep session information retrieved from the message.
	// XXX - should return error if required IE is missing.
	if ie := csReqFromMME.IMSI; ie != nil {
		imsi := ie.IMSI()
		// remove previous session for the same subscriber if exists.
		sess, err := s11Conn.GetSessionByIMSI(imsi)
		if err != nil {
			if err == v2.ErrUnknownIMSI {
				// whole new session. just ignore.
			} else {
				return errors.Wrap(err, "got something unexpected")
			}
		} else {
			s11Conn.RemoveSession(sess)
		}

		s11Session.IMSI = imsi
	} else {
		return &v2.ErrRequiredIEMissing{Type: ies.IMSI}
	}
	if ie := csReqFromMME.MSISDN; ie != nil {
		s11Session.MSISDN = ie.MSISDN()
	} else {
		return &v2.ErrRequiredIEMissing{Type: ies.MSISDN}
	}
	if ie := csReqFromMME.MEI; ie != nil {
		s11Session.IMEI = ie.MobileEquipmentIdentity()
	} else {
		return &v2.ErrRequiredIEMissing{Type: ies.MobileEquipmentIdentity}
	}
	if ie := csReqFromMME.APN; ie != nil {
		s11Bearer.APN = ie.AccessPointName()
	} else {
		return &v2.ErrRequiredIEMissing{Type: ies.AccessPointName}
	}
	if ie := csReqFromMME.ServingNetwork; ie != nil {
		s11Session.MCC = ie.MCC()
		s11Session.MNC = ie.MNC()
	} else {
		return &v2.ErrRequiredIEMissing{Type: ies.ServingNetwork}
	}
	if ie := csReqFromMME.RATType; ie != nil {
		s11Session.RATType = ie.RATType()
	} else {
		return &v2.ErrRequiredIEMissing{Type: ies.RATType}
	}
	s11Conn.AddSession(s11Session)

	s5cIP := laddr.IP.String()
	s5cFTEID := sgw.s5cConn.NewFTEID(v2.IFTypeS5S8SGWGTPC, s5cIP, "")
	s5uFTEID := sgw.s5cConn.NewFTEID(v2.IFTypeS5S8SGWGTPU, s5cIP, "").WithInstance(2)

	s5Session, err := sgw.s5cConn.CreateSession(
		raddr,
		csReqFromMME.IMSI, csReqFromMME.MSISDN, csReqFromMME.MEI, csReqFromMME.ServingNetwork,
		csReqFromMME.RATType, csReqFromMME.IndicationFlags, s5cFTEID, csReqFromMME.PGWS5S8FTEIDC,
		csReqFromMME.APN, csReqFromMME.SelectionMode, csReqFromMME.PDNType, csReqFromMME.PAA,
		csReqFromMME.APNRestriction, csReqFromMME.AMBR, csReqFromMME.ULI,
		ies.NewBearerContext(
			ies.NewEPSBearerID(5),
			s5uFTEID,
			ies.NewBearerQoS(1, 2, 1, 0xff, 0, 0, 0, 0),
		),
		csReqFromMME.MMEFQCSID,
		ies.NewFullyQualifiedCSID(s5cIP, 1).WithInstance(1),
	)
	if err != nil {
		return err
	}
	s5Session.AddTEID(s5uFTEID.InterfaceType(), s5uFTEID.TEID())
	sgw.s5cConn.AddSession(s5Session)

	sgw.loggerCh <- fmt.Sprintf("Sent Create Session Request to %s for %s", pgwAddrString, s5Session.IMSI)

	doneCh := make(chan struct{})
	failCh := make(chan error)
	go func() {
		var csRspFromSGW *messages.CreateSessionResponse
		s11mmeTEID, err := s11Session.GetTEID(v2.IFTypeS11MMEGTPC)
		if err != nil {
			failCh <- err
			return
		}

		message, err := s11Session.WaitMessage(5 * time.Second)
		if err != nil {
			csRspFromSGW = messages.NewCreateSessionResponse(
				s11mmeTEID, 0,
				ies.NewCause(v2.CauseNoResourcesAvailable, 0, 0, 0, nil),
			)

			if err := s11Conn.RespondTo(mmeAddr, csReqFromMME, csRspFromSGW); err != nil {
				failCh <- err
				return
			}
			sgw.loggerCh <- fmt.Sprintf(
				"Sent %s with failure code: %d, target subscriber: %s",
				csRspFromSGW.MessageTypeName(), v2.CausePGWNotResponding, s11Session.IMSI,
			)
			failCh <- err
			return

		}

		var csRspFromPGW *messages.CreateSessionResponse
		switch m := message.(type) {
		case *messages.CreateSessionResponse:
			// move forward
			csRspFromPGW = m
		default:
			failCh <- v2.ErrUnexpectedType
			return
		}
		// if everything in CreateSessionResponse seems OK, relay it to MME.
		s11IP, _, err := net.SplitHostPort(*s11)
		if err != nil {
			return
		}
		senderFTEID := s11Conn.NewFTEID(v2.IFTypeS11S4SGWGTPC, s11IP, "")
		s1usgwFTEID := s11Conn.NewFTEID(v2.IFTypeS1USGWGTPU, s11IP, "")
		csRspFromSGW = csRspFromPGW
		csRspFromSGW.SenderFTEIDC = senderFTEID
		csRspFromSGW.SGWFQCSID = ies.NewFullyQualifiedCSID(laddr.IP.String(), 1).WithInstance(1)
		csRspFromSGW.BearerContextsCreated.Add(s1usgwFTEID)
		csRspFromSGW.BearerContextsCreated.Remove(ies.ChargingID, 0)
		csRspFromSGW.SetTEID(s11mmeTEID)
		csRspFromSGW.SetLength()

		if err := s11Conn.RespondTo(mmeAddr, csReqFromMME, csRspFromSGW); err != nil {
			failCh <- err
			return
		}
		s11Session.AddTEID(senderFTEID.InterfaceType(), senderFTEID.TEID())
		s11Session.AddTEID(s1usgwFTEID.InterfaceType(), s1usgwFTEID.TEID())

		s11sgwTEID, err := s11Session.GetTEID(v2.IFTypeS11S4SGWGTPC)
		if err != nil {
			failCh <- err
			return
		}
		s5cpgwTEID, err := s5Session.GetTEID(v2.IFTypeS5S8PGWGTPC)
		if err != nil {
			failCh <- err
			return
		}
		s5csgwTEID, err := s5Session.GetTEID(v2.IFTypeS5S8SGWGTPC)
		if err != nil {
			failCh <- err
			return
		}
		sgw.loggerCh <- fmt.Sprintf(
			"Session created with MME and P-GW for Subscriber: %s;\n\tS11 MME:  %s, TEID->: %#x, TEID<-: %#x\n\tS5C P-GW: %s, TEID->: %#x, TEID<-: %#x",
			s5Session.Subscriber.IMSI, mmeAddr, s11mmeTEID, s11sgwTEID, pgwAddrString, s5cpgwTEID, s5csgwTEID,
		)
		doneCh <- struct{}{}
	}()

	select {
	case <-doneCh:
		if s11Session.Activate(); err != nil {
			sgw.loggerCh <- errors.Wrap(err, "Error").Error()
			s11Conn.RemoveSession(s11Session)
			return nil
		}
		return nil
	case err := <-failCh:
		s11Conn.RemoveSession(s11Session)
		return err
	case <-time.After(10 * time.Second):
		s11Conn.RemoveSession(s11Session)
		return v2.ErrTimeout
	}
}

func handleModifyBearerRequest(s11Conn *v2.Conn, mmeAddr net.Addr, msg messages.Message) error {
	sgw.loggerCh <- fmt.Sprintf("Received %s from %s", msg.MessageTypeName(), mmeAddr)

	s11Session, err := s11Conn.GetSessionByTEID(msg.TEID())
	if err != nil {
		return err
	}
	s5cSession, err := sgw.s5cConn.GetSessionByIMSI(s11Session.IMSI)
	if err != nil {
		return err
	}
	s1uBearer := s11Session.GetDefaultBearer()
	s5uBearer := s5cSession.GetDefaultBearer()

	// assert type to refer to the struct field specific to the message.
	// in general, no need to check if it can be type-asserted, as long as the MessageType is
	// specified correctly in AddHandler().
	mbReqFromMME := msg.(*messages.ModifyBearerRequest)
	if brCtxIE := mbReqFromMME.BearerContextsToBeModified; brCtxIE != nil {
		for _, ie := range brCtxIE.ChildIEs {
			switch ie.Type {
			case ies.Indication:
				// do nothing in this example.
				// S-GW should change its beahavior based on indication flags like;
				//  - pass Modify Bearer Request to P-GW if handover is indicated.
				//  - XXX...
			case ies.FullyQualifiedTEID:
				if err := handleFTEIDU(ie, s11Session, s1uBearer); err != nil {
					return err
				}
			}
		}
	}

	s11mmeTEID, err := s11Session.GetTEID(v2.IFTypeS11MMEGTPC)
	if err != nil {
		return err
	}
	s1usgwTEID, err := s11Session.GetTEID(v2.IFTypeS1USGWGTPU)
	if err != nil {
		return err
	}
	s5usgwTEID, err := s5cSession.GetTEID(v2.IFTypeS5S8SGWGTPU)
	if err != nil {
		return err
	}
	sgw.s1uConn.RelayTo(sgw.s5uConn, s1usgwTEID, s5uBearer.OutgoingTEID(), s5uBearer.RemoteAddress())
	sgw.s5uConn.RelayTo(sgw.s1uConn, s5usgwTEID, s1uBearer.OutgoingTEID(), s1uBearer.RemoteAddress())

	s1uIP, _, err := net.SplitHostPort(*s1u)
	if err != nil {
		return err
	}
	mbRspFromSGW := messages.NewModifyBearerResponse(
		s11mmeTEID, 0,
		ies.NewCause(v2.CauseRequestAccepted, 0, 0, 0, nil),
		ies.NewBearerContext(
			ies.NewCause(v2.CauseRequestAccepted, 0, 0, 0, nil),
			ies.NewEPSBearerID(s1uBearer.EBI),
			ies.NewFullyQualifiedTEID(v2.IFTypeS1USGWGTPU, s1usgwTEID, s1uIP, ""),
		),
	)

	if err := s11Conn.RespondTo(mmeAddr, msg, mbRspFromSGW); err != nil {
		return err
	}

	sgw.loggerCh <- fmt.Sprintf(
		"Started listening on U-Plane for Subscriber: %s;\n\tS1-U: %s\n\tS5-U: %s",
		s11Session.IMSI, *s1u, *s5u,
	)
	return nil
}

func handleDeleteSessionRequest(s11Conn *v2.Conn, mmeAddr net.Addr, msg messages.Message) error {
	sgw.loggerCh <- fmt.Sprintf("Received %s from %s", msg.MessageTypeName(), mmeAddr)

	// assert type to refer to the struct field specific to the message.
	// in general, no need to check if it can be type-asserted, as long as the MessageType is
	// specified correctly in AddHandler().
	dsReqFromMME := msg.(*messages.DeleteSessionRequest)

	s11Session, err := s11Conn.GetSessionByTEID(msg.TEID())
	if err != nil {
		return err
	}

	s5Session, err := sgw.s5cConn.GetSessionByIMSI(s11Session.IMSI)
	if err != nil {
		return err
	}

	s5cpgwTEID, err := s5Session.GetTEID(v2.IFTypeS5S8PGWGTPC)
	if err != nil {
		return err
	}

	if err := sgw.s5cConn.DeleteSession(
		s5cpgwTEID,
		ies.NewEPSBearerID(s5Session.GetDefaultBearer().EBI),
	); err != nil {
		return err
	}

	// wait for response from P-GW for 5 seconds
	doneCh := make(chan struct{})
	failCh := make(chan error)
	go func() {
		var dsRspFromSGW *messages.DeleteSessionResponse
		s11mmeTEID, err := s11Session.GetTEID(v2.IFTypeS11MMEGTPC)
		if err != nil {
			failCh <- err
			return
		}

		message, err := s11Session.WaitMessage(5 * time.Second)
		if err != nil {
			dsRspFromSGW = messages.NewDeleteSessionResponse(
				s11mmeTEID, 0,
				ies.NewCause(v2.CausePGWNotResponding, 0, 0, 0, nil),
			)

			if err := s11Conn.RespondTo(mmeAddr, dsReqFromMME, dsRspFromSGW); err != nil {
				failCh <- err
				return
			}
			sgw.loggerCh <- fmt.Sprintf(
				"Sent %s with failure code: %d, target subscriber: %s",
				dsRspFromSGW.MessageTypeName(), v2.CausePGWNotResponding, s11Session.IMSI,
			)
			failCh <- err
			return
		}

		// use the cause as it is.
		switch m := message.(type) {
		case *messages.DeleteSessionResponse:
			// move forward
			dsRspFromSGW = m
		default:
			failCh <- v2.ErrUnexpectedType
			return
		}

		dsRspFromSGW.SetTEID(s11mmeTEID)
		if err := s11Conn.RespondTo(mmeAddr, msg, dsRspFromSGW); err != nil {
			failCh <- err
			return
		}

		sgw.loggerCh <- fmt.Sprintf("Session deleted for Subscriber: %s", s11Session.IMSI)
		s11Conn.RemoveSession(s11Session)
		doneCh <- struct{}{}
	}()
	select {
	case <-doneCh:
		return nil
	case err := <-failCh:
		return err
	}
}

func handleDeleteBearerResponse(s11Conn *v2.Conn, mmeAddr net.Addr, msg messages.Message) error {
	sgw.loggerCh <- fmt.Sprintf("Received %s from %s", msg.MessageTypeName(), mmeAddr)

	s11Session, err := s11Conn.GetSessionByTEID(msg.TEID())
	if err != nil {
		return err
	}

	s5Session, err := sgw.s5cConn.GetSessionByIMSI(s11Session.IMSI)
	if err != nil {
		return err
	}

	if err := v2.PassMessageTo(s5Session, msg, 5*time.Second); err != nil {
		return err
	}

	// remove bearer in handleDeleteBearerRequest instead of doing here,
	// as Delete Bearer Request does not necessarily have EBI.
	return nil
}

func handleFTEIDU(ie *ies.IE, session *v2.Session, bearer *v2.Bearer) error {
	if ie.Type != ies.FullyQualifiedTEID {
		return v2.ErrUnexpectedType
	}

	addr, err := net.ResolveUDPAddr("udp", ie.IPAddress()+":2152")
	if err != nil {
		return err
	}
	bearer.SetRemoteAddress(addr)
	bearer.SetOutgoingTEID(ie.TEID())

	session.AddTEID(ie.InterfaceType(), ie.TEID())
	return nil
}
