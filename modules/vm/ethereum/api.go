package ethereum

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/big"
	"time"

	"github.com/spf13/cast"

	"github.com/cosmos/cosmos-sdk"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/commands"
	"github.com/cosmos/cosmos-sdk/modules/base"
	"github.com/cosmos/cosmos-sdk/stack"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/tendermint/go-crypto"
	"github.com/tendermint/go-wire"
	"github.com/tendermint/go-wire/data"
	rpcclient "github.com/tendermint/tendermint/rpc/client"
	ctypes "github.com/tendermint/tendermint/rpc/core/types"
	cmn "github.com/tendermint/tmlibs/common"

	"github.com/CyberMiles/travis/modules/auth"
	"github.com/CyberMiles/travis/modules/coin"
	"github.com/CyberMiles/travis/modules/keys"
	"github.com/CyberMiles/travis/modules/nonce"
	"github.com/CyberMiles/travis/modules/stake"
)

// We must implement our own net service since we don't have access to `internal/ethapi`

// NetRPCService mirrors the implementation of `internal/ethapi`
// #unstable
type NetRPCService struct {
	networkVersion uint64
}

// NewNetRPCService creates a new net API instance.
// #unstable
func NewNetRPCService(networkVersion uint64) *NetRPCService {
	return &NetRPCService{networkVersion}
}

// Listening returns an indication if the node is listening for network connections.
// #unstable
func (s *NetRPCService) Listening() bool {
	return true // always listening
}

// PeerCount returns the number of connected peers
// #unstable
func (s *NetRPCService) PeerCount() hexutil.Uint {
	return hexutil.Uint(0)
}

// Version returns the current ethereum protocol version.
// #unstable
func (s *NetRPCService) Version() string {
	return fmt.Sprintf("%d", s.networkVersion)
}

// CmtRPCService offers cmt related RPC methods
type CmtRPCService struct {
	backend *Backend
}

func NewCmtRPCService(b *Backend) *CmtRPCService {
	return &CmtRPCService{
		backend: b,
	}
}

func (s *CmtRPCService) GetBlock(height uint64) (*ctypes.ResultBlock, error) {
	h := cast.ToInt64(height)
	return s.backend.localClient.Block(&h)
}

func (s *CmtRPCService) GetTransaction(hash string) (*ctypes.ResultTx, error) {
	bkey, err := hex.DecodeString(cmn.StripHex(hash))
	if err != nil {
		return nil, err
	}
	return s.backend.localClient.Tx(bkey, false)
}

func (s *CmtRPCService) GetTransactionFromBlock(height uint64, index int64) (*ctypes.ResultTx, error) {
	h := cast.ToInt64(height)
	block, err := s.backend.localClient.Block(&h)
	if err != nil {
		return nil, err
	}
	if index >= block.Block.NumTxs {
		return nil, errors.New(fmt.Sprintf("No transaction in block %d, index %d. ", height, index))
	}
	hash := block.Block.Txs[index].Hash()
	return s.GetTransaction(hex.EncodeToString(hash))
}

// StakeRPCService offers stake related RPC methods
type StakeRPCService struct {
	backend *Backend
	am      *accounts.Manager
}

// NewStakeRPCAPI create a new StakeRPCAPI.
func NewStakeRPCService(b *Backend) *StakeRPCService {
	return &StakeRPCService{
		backend: b,
		am:      b.ethereum.AccountManager(),
	}
}

func (s *StakeRPCService) getChainID() (string, error) {
	if s.backend.chainID == "" {
		return "", errors.New("Empty chain id. Please wait for tendermint to finish starting up. ")
	}

	return s.backend.chainID, nil
}

// copied from ethapi/api.go
func (s *StakeRPCService) UnlockAccount(addr common.Address, password string, duration *uint64) (bool, error) {
	const max = uint64(time.Duration(math.MaxInt64) / time.Second)
	var d time.Duration
	if duration == nil {
		d = 300 * time.Second
	} else if *duration > max {
		return false, errors.New("unlock duration too large")
	} else {
		d = time.Duration(*duration) * time.Second
	}
	err := fetchKeystore(s.am).TimedUnlock(accounts.Account{Address: addr}, password, d)
	return err == nil, err
}

// fetchKeystore retrives the encrypted keystore from the account manager.
func fetchKeystore(am *accounts.Manager) *keystore.KeyStore {
	return am.Backends(keystore.KeyStoreType)[0].(*keystore.KeyStore)
}

type DeclareCandidacyArgs struct {
	Sequence uint32 `json:"sequence"`
	From     string `json:"from"`
	PubKey   string `json:"pubKey"`
}

func (s *StakeRPCService) DeclareCandidacy(args DeclareCandidacyArgs) (*ctypes.ResultBroadcastTxCommit, error) {
	tx, err := s.prepareDeclareCandidacyTx(args)
	if err != nil {
		return nil, err
	}
	return s.broadcastTx(tx)
}

func (s *StakeRPCService) prepareDeclareCandidacyTx(args DeclareCandidacyArgs) (sdk.Tx, error) {
	pubKey, err := stake.GetPubKey(args.PubKey)
	if err != nil {
		return sdk.Tx{}, err
	}
	tx := stake.NewTxDeclare(pubKey)
	return s.wrapAndSignTx(tx, args.From, args.Sequence)
}

type ProposeSlotArgs struct {
	Sequence    uint32 `json:"sequence"`
	From        string `json:"from"`
	PubKey      string `json:"pubKey"`
	Amount      int64  `json:"amount"`
	ProposedRoi int64  `json:"proposedRoi"`
}

func (s *StakeRPCService) ProposeSlot(args ProposeSlotArgs) (*ctypes.ResultBroadcastTxCommit, error) {
	tx, err := s.prepareProposeSlotTx(args)
	if err != nil {
		return nil, err
	}
	return s.broadcastTx(tx)
}

func (s *StakeRPCService) prepareProposeSlotTx(args ProposeSlotArgs) (sdk.Tx, error) {
	pubKey, err := stake.GetPubKey(args.PubKey)
	if err != nil {
		return sdk.Tx{}, err
	}
	tx := stake.NewTxProposeSlot(pubKey, args.Amount, args.ProposedRoi)
	return s.wrapAndSignTx(tx, args.From, args.Sequence)
}

type AcceptSlotArgs struct {
	Sequence uint32 `json:"sequence"`
	From     string `json:"from"`
	Amount   int64  `json:"amount"`
	SlotId   string `json:"slotId"`
}

func (s *StakeRPCService) AcceptSlot(args AcceptSlotArgs) (*ctypes.ResultBroadcastTxCommit, error) {
	tx, err := s.prepareAcceptSlotTx(args)
	if err != nil {
		return nil, err
	}
	return s.broadcastTx(tx)
}

func (s *StakeRPCService) prepareAcceptSlotTx(args AcceptSlotArgs) (sdk.Tx, error) {
	tx := stake.NewTxAcceptSlot(args.Amount, args.SlotId)
	return s.wrapAndSignTx(tx, args.From, args.Sequence)
}

type WithdrawSlotArgs struct {
	Sequence uint32 `json:"sequence"`
	From     string `json:"from"`
	Amount   int64  `json:"amount"`
	SlotId   string `json:"slotId"`
}

func (s *StakeRPCService) WithdrawSlot(args WithdrawSlotArgs) (*ctypes.ResultBroadcastTxCommit, error) {
	tx, err := s.prepareWithdrawSlotTx(args)
	if err != nil {
		return nil, err
	}
	return s.broadcastTx(tx)
}

func (s *StakeRPCService) prepareWithdrawSlotTx(args WithdrawSlotArgs) (sdk.Tx, error) {
	tx := stake.NewTxWithdrawSlot(args.Amount, args.SlotId)
	return s.wrapAndSignTx(tx, args.From, args.Sequence)
}

type CancelSlotArgs struct {
	Sequence uint32 `json:"sequence"`
	From     string `json:"from"`
	PubKey   string `json:"pubKey"`
	SlotId   string `json:"slotId"`
}

func (s *StakeRPCService) CancelSlot(args CancelSlotArgs) (*ctypes.ResultBroadcastTxCommit, error) {
	tx, err := s.prepareCancelSlotTx(args)
	if err != nil {
		return nil, err
	}
	return s.broadcastTx(tx)
}

func (s *StakeRPCService) prepareCancelSlotTx(args CancelSlotArgs) (sdk.Tx, error) {
	pubKey, err := stake.GetPubKey(args.PubKey)
	if err != nil {
		return sdk.Tx{}, err
	}
	tx := stake.NewTxCancelSlot(pubKey, args.SlotId)
	return s.wrapAndSignTx(tx, args.From, args.Sequence)
}

func (s *StakeRPCService) wrapAndSignTx(tx sdk.Tx, address string, sequence uint32) (sdk.Tx, error) {
	// wrap
	// only add the actual signer to the nonce
	signers := []sdk.Actor{getSignerAct(address)}
	if sequence <= 0 {
		// calculate default sequence
		err := s.getSequence(signers, &sequence)
		if err != nil {
			return sdk.Tx{}, err
		}
		sequence = sequence + 1
	}
	tx = nonce.NewTx(sequence, signers, tx)

	chainID, err := s.getChainID()
	if err != nil {
		return sdk.Tx{}, err
	}
	tx = base.NewChainTx(chainID, 0, tx)
	tx = auth.NewSig(tx).Wrap()

	// sign
	err = s.signTx(tx, address)
	if err != nil {
		return sdk.Tx{}, err
	}
	return tx, err
}

func (s *StakeRPCService) getSequence(signers []sdk.Actor, sequence *uint32) error {
	key := stack.PrefixedKey(nonce.NameNonce, nonce.GetSeqKey(signers))
	result, err := s.backend.localClient.ABCIQuery("/key", key)
	if err != nil {
		return err
	}

	if len(result.Response.Value) == 0 {
		return nil
	}
	return wire.ReadBinaryBytes(result.Response.Value, sequence)
}

// sign the transaction with private key
func (s *StakeRPCService) signTx(tx sdk.Tx, address string) error {
	// validate tx client-side
	err := tx.ValidateBasic()
	if err != nil {
		return err
	}

	if sign, ok := tx.Unwrap().(keys.Signable); ok {
		if address == "" {
			return errors.New("address is required to sign tx")
		}
		err := s.sign(sign, address)
		if err != nil {
			return err
		}
	}
	return err
}

func (s *StakeRPCService) sign(data keys.Signable, address string) error {
	ethTx := types.NewTransaction(
		0,
		common.Address([20]byte{}),
		big.NewInt(0),
		big.NewInt(0),
		big.NewInt(0),
		data.SignBytes(),
	)

	addr := common.HexToAddress(address)
	account := accounts.Account{Address: addr}
	wallet, err := s.am.Find(account)
	signed, err := wallet.SignTx(account, ethTx, big.NewInt(15)) //TODO: use defaultEthChainId
	if err != nil {
		return err
	}

	return data.Sign(signed)
}

func (s *StakeRPCService) broadcastTx(tx sdk.Tx) (*ctypes.ResultBroadcastTxCommit, error) {
	key := wire.BinaryBytes(tx)
	return s.backend.localClient.BroadcastTxCommit(key)
}

func getSignerAct(address string) (res sdk.Actor) {
	// this could be much cooler with multisig...
	signer := common.HexToAddress(address)
	res = auth.SigPerm(signer.Bytes())
	return res
}

type StakeQueryResult struct {
	Height int64       `json:"height"`
	Data   interface{} `json:"data"`
}

func (s *StakeRPCService) QueryValidators(height uint64) (*StakeQueryResult, error) {
	var pks []crypto.PubKey
	key := stack.PrefixedKey(stake.Name(), stake.CandidatesPubKeysKey)
	h, err := s.getParsed("/key", key, &pks, height)
	if err != nil {
		return nil, err
	}

	return &StakeQueryResult{h, pks}, nil
}

func (s *StakeRPCService) QueryValidator(pubkey string, height uint64) (*StakeQueryResult, error) {
	pk, err := stake.GetPubKey(pubkey)
	if err != nil {
		return nil, err
	}

	var candidate stake.Candidate
	key := stack.PrefixedKey(stake.Name(), stake.GetCandidateKey(pk))
	h, err := s.getParsed("/key", key, &candidate, height)
	if err != nil {
		return nil, err
	}

	return &StakeQueryResult{h, candidate}, nil
}

func (s *StakeRPCService) QuerySlots(address string, height uint64) (*StakeQueryResult, error) {
	delegator, err := commands.ParseActor(address)
	if err != nil {
		return nil, err
	}
	delegator = coin.ChainAddr(delegator)

	var candidates []crypto.PubKey
	key := stack.PrefixedKey(stake.Name(), stake.GetDelegatorBondsKey(delegator))
	h, err := s.getParsed("/key", key, &candidates, height)
	if err != nil {
		return nil, err
	}

	return &StakeQueryResult{h, candidates}, nil
}

func (s *StakeRPCService) QuerySlot(slotId string, height uint64) (*StakeQueryResult, error) {
	var slot stake.Slot
	h, err := s.getParsed("/slot", []byte(slotId), &slot, height)
	if err != nil {
		return nil, err
	}

	return &StakeQueryResult{h, slot}, nil
}

func (s *StakeRPCService) QueryDelegator(address string, height uint64) (*StakeQueryResult, error) {
	var slotDelegates []*stake.SlotDelegate
	h, err := s.getParsed("/delegator", []byte(address), &slotDelegates, height)
	if err != nil {
		return nil, err
	}

	return &StakeQueryResult{h, slotDelegates}, nil
}

func (s *StakeRPCService) getParsed(path string, key []byte, data interface{}, height uint64) (int64, error) {
	bs, h, err := s.get(path, key, cast.ToInt64(height))
	if err != nil {
		return 0, err
	}
	if len(bs) == 0 {
		return h, client.ErrNoData()
	}
	err = wire.ReadBinaryBytes(bs, data)
	if err != nil {
		return 0, err
	}
	return h, nil
}

func (s *StakeRPCService) get(path string, key []byte, height int64) (data.Bytes, int64, error) {
	node := s.backend.localClient
	resp, err := node.ABCIQueryWithOptions(path, key,
		rpcclient.ABCIQueryOptions{Trusted: true, Height: int64(height)})
	if resp == nil {
		return nil, height, err
	}
	return data.Bytes(resp.Response.Value), resp.Response.Height, err
}