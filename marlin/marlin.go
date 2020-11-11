package marlin

import (
	"bufio"
	"io"
	"net"
	"os"
	// "fmt"
	// "io/ioutil"
	"time"
	"encoding/binary"

	proto "github.com/golang/protobuf/proto"
	log "github.com/sirupsen/logrus"

	"github.com/supragya/tendermint_connector/types"

	// Protocols
	wireProtocol "github.com/supragya/tendermint_connector/marlin/protocols/tmDataTransferProtocolv1"
)


var currentlyServicing 	uint32
// ALlowServicedChainMessages allows handlers to register a single chain
// to allow through marlin side connector. Initially there is no blockchain message
// allowed to passthrough. One can register the chainId to look for in messages
// correspoding to chains available in types.servicedChains to allow.
func AllowServicedChainMessages(servicedChainId uint32) {
	currentlyServicing = servicedChainId
}

type MarlinHandler struct {
	marlinConn			*bufio.ReadWriter
	marlinAddr			string
	marlinTo			chan types.MarlinMessage
	marlinFrom			chan types.MarlinMessage
	signalConnError		chan struct{}
	signalShutSend		chan struct{}
	signalShutRecv		chan struct{}
}

// Run acts as the entry point to marlin side connection logic.
// Marlin Side Connector requires two channels for sending / recieving messages
// from peer side connector.
func Run(marlinAddr string, marlinTo chan types.MarlinMessage, marlinFrom chan types.MarlinMessage) {
	log.Info("Starting Marlin TCP Bridge Handler")

	for {
		handler, err := createMarlinHandler(marlinAddr, marlinTo, marlinFrom)

		if err != nil {
			log.Error("Error encountered while creating Marlin Handler: ", err)
			os.Exit(1)
		}

		err = handler.dialTCPBridge()
		if err != nil {
			log.Error("Connection establishment with Marlin TCP Bridge unsuccessful: ", err)
			goto REATTEMPT_CONNECTION
		}

		handler.beginServicing()

		select {
		case <- handler.signalConnError:
			handler.signalShutSend<- struct{}{}
			handler.signalShutRecv<- struct{}{}
			goto REATTEMPT_CONNECTION			
		}

		REATTEMPT_CONNECTION:
		log.Info("Error encountered with connection to the Marlin TCP Bridge. Attempting reconnect post 1 second.")
		time.Sleep(1 * time.Second)
	}
	// conn, err := net.Dial("tcp", marlinAddr)
	// if err != nil {
	// 	log.Error("Unable to connect to Marlin multicast TCP bridge at ", marlinAddr, ". Closing application")
	// 	os.Exit(2)
	// }

	// marlinConn = bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))

	// go sendRoutine(marlinTo)
	// go recvRoutine(marlinFrom)
}

func createMarlinHandler(marlinAddr string, 
			marlinTo chan types.MarlinMessage, 
			marlinFrom chan types.MarlinMessage) (MarlinHandler, error) {
	return MarlinHandler{
		marlinAddr:	marlinAddr,
		marlinTo: marlinTo,
		marlinFrom:	marlinFrom,
		signalConnError:	 make(chan struct{}, 1),
		signalShutSend:		 make(chan struct{}, 1),
		signalShutRecv:		 make(chan struct{}, 1),
	}, nil
}

func (h *MarlinHandler) dialTCPBridge() error {
	conn, err := net.DialTimeout("tcp", h.marlinAddr, 2000 * time.Millisecond)
	if err != nil {
		return err
	}
	h.marlinConn = bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	return nil
}

func (h *MarlinHandler) beginServicing() {
	go h.sendRoutine()
	go h.recvRoutine()
}

// sendRoutine is the routine that reads data from peer side connector and makes it available to marlin relay
// Messages on wire are encoded with Marlin Tendermint Data Transfer Protocol v1 prepended with length of message
// Messages from peer connector on channel is defined using types.marlinMessage
func (h *MarlinHandler) sendRoutine() {
	log.Info("Marlin <- Connector Routine started")

	for msg := range h.marlinTo {
		select {
		case <-h.signalShutSend:
			h.marlinTo <- msg
			log.Info("Marlin <- Connector Routine shutdown")
			return 
		default:
		}

		if msg.ChainID != currentlyServicing {
			log.Error("Marlin <- Connector Discarding message to marlin relay, chain is not allowed. Chain ID: ", msg.ChainID)
			continue
		}

		messageLen := make([]byte, 2)

		// Making PacketMsg Proto3 PacketMsg
		wirePackets := []*wireProtocol.PacketMsg{}
		for _, pkt := range msg.Packets {
			wirePackets = append(wirePackets, &wireProtocol.PacketMsg{
												Eof:	   pkt.EOF,
												DataBytes: pkt.Bytes,
											})
		}

		sendData := &wireProtocol.TendermintMessage{
			ChainId: msg.ChainID,
			Channel: uint32(msg.Channel),
			Packets: wirePackets,
		}

		proto3Marshalled, err := proto.Marshal(sendData)
		if err != nil {
			log.Error("Marlin <- Connector Error encountered while marshalling sendData.")
			h.signalConnError <- struct{}{}
			return
		}

		binary.LittleEndian.PutUint16(messageLen, uint16(len(proto3Marshalled)))

		encodedData := append(messageLen, proto3Marshalled...)

		n, err := h.marlinConn.Write(encodedData)
		if err != nil {
			log.Error("Marlin <- Connector Error encountered when writing to marlin TCP Bridge: ", err)
			h.signalConnError <- struct{}{}
			return
		} else if n < len(encodedData) {
			log.Error("Marlin <- Connector Too few data pushed to marlin TCP Bridge: ", n, " vs ", len(encodedData))
			h.signalConnError <- struct{}{}
			return
		}

		err = h.marlinConn.Flush()
		if err != nil {
			log.Error("Marlin <- Connector Error flushing marlinWriter buffer, err: ", err)
			h.signalConnError <- struct{}{}
			return
		}

		log.Debug("Marlin <- Connector transferred stat: ", len(encodedData), " ", encodedData)
	}
}

// recvRoutine is the routine that read data from marlin relay and makes it available to peer side connector.
// Messages on wire are encoded with Marlin Tendermint Data Transfer Protocol v1 prepended with length of message
// Messages pushed to peer connector on channel is defined using types.marlinMessage
func (h *MarlinHandler) recvRoutine() {
	log.Info("Marlin -> Connector Routine started")
	for {
		select {
		case <-h.signalShutRecv:
			log.Info("Marlin -> Connector Routine shutdown")
			return 
		default:
		}
		messageLenByte0, err1 := h.marlinConn.ReadByte()
		messageLenByte1, err2 := h.marlinConn.ReadByte()

		if err1 != nil || err2 != nil {
			log.Error("Marlin -> Connector Error reading message size from the buffer")
			h.signalConnError <- struct{}{}
			return
		}

		messageLen := binary.LittleEndian.Uint16([]byte{messageLenByte0, messageLenByte1})

		var buffer = make([]byte, messageLen)
		n, err := io.ReadFull(h.marlinConn, buffer)
		if err != nil {
			log.Error("Marlin -> Connector Error reading message of size ", messageLen, " instead read ", n)
			h.signalConnError <- struct{}{}
			return
		}

		tmMessage := wireProtocol.TendermintMessage{}
		proto.Unmarshal(buffer, &tmMessage)
		log.Debug("Marlin -> Connector recieved message: ", buffer, " ", tmMessage)

		if tmMessage.GetChainId() == currentlyServicing && messageLen > 0 {
			internalPackets := []types.PacketMsg{}
			for _, pkt := range tmMessage.GetPackets() {
				internalPackets = append(internalPackets, types.PacketMsg{
															ChannelID: tmMessage.GetChannel(),
															EOF:	pkt.GetEof(),
															Bytes:	pkt.GetDataBytes(),
														})
			}
			h.marlinFrom <- types.MarlinMessage{
				ChainID: tmMessage.GetChainId(),
				Channel: byte(tmMessage.GetChannel()),
				Packets: internalPackets,
			}
		}
	}
}
