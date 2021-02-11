package irisnet

import (
	"net"
	"sync"

	"github.com/gogo/protobuf/proto"
	tmp2p "github.com/tendermint/tendermint/proto/p2p"
	lru "github.com/hashicorp/golang-lru"
	"github.com/tendermint/tendermint/crypto/ed25519"
	"github.com/supragya/tendermint_connector/chains/irisnet/conn"
	marlinTypes "github.com/supragya/tendermint_connector/types"
)

type TendermintHandler struct {
	servicedChainId      uint32
	listenPort           int
	isConnectionOutgoing bool
	peerAddr             string
	rpcAddr              string
	privateKey           ed25519.PrivKeyEd25519
	baseConnection       net.Conn
	validatorCache		 *lru.TwoQueueCache
	maxValidHeight 			int64
	secretConnection     *conn.SecretConnection
	marlinTo             chan marlinTypes.MarlinMessage
	marlinFrom           chan marlinTypes.MarlinMessage
	channelBuffer        map[byte][]marlinTypes.PacketMsg
	peerNodeInfo         DefaultNodeInfo
	p2pConnection        P2PConnection
	throughput           throughPutData
	signalConnError      chan struct{}
	signalShutSend       chan struct{}
	signalShutRecv       chan struct{}
	signalShutThroughput chan struct{}
	//no codec file needed, In protobuf the codec file could be considered as
	// proto file that is declared in github.com/gogo/protobuf/proto
}

type throughPutData struct {
	isDataConnect bool
	toTMCore   map[string]uint32
	fromTMCore map[string]uint32
	spam	   map[string]uint32
	mu         sync.Mutex
}

type keyData struct {
	Chain            string
	IdString         string
	PrivateKeyString string
	PublicKeyString  string
	PrivateKey       [64]byte
	PublicKey        [32]byte
}

type Validator struct {
	PublicKey	ed25519.PubKeyEd25519
	Address		string
}