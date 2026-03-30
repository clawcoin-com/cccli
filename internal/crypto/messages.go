// messages.go contains protobuf message encoders for Cosmos SDK and application-specific messages.
// Each encoder matches the corresponding .proto definition using hand-written wire format.
package crypto

// ============================================================================
// Cosmos SDK common types
// ============================================================================

// EncodeAny encodes a google.protobuf.Any message.
// Any { type_url: string (field 1), value: bytes (field 2) }
func EncodeAny(typeURL string, value []byte) []byte {
	w := NewProtoWriter()
	w.WriteString(1, typeURL)
	w.WriteBytes(2, value)
	return w.Bytes()
}

// EncodePubKeySecp256k1 encodes a cosmos.crypto.secp256k1.PubKey.
// PubKey { key: bytes (field 1) }
func EncodePubKeySecp256k1(pubKeyBytes []byte) []byte {
	w := NewProtoWriter()
	w.WriteBytes(1, pubKeyBytes)
	return w.Bytes()
}

// EncodeCoin encodes a cosmos.base.v1beta1.Coin.
// Coin { denom: string (field 1), amount: string (field 2) }
func EncodeCoin(denom, amount string) []byte {
	w := NewProtoWriter()
	w.WriteString(1, denom)
	w.WriteString(2, amount)
	return w.Bytes()
}

// ============================================================================
// Cosmos SDK transaction structure
// ============================================================================

// EncodeFee encodes a cosmos.tx.v1beta1.Fee.
// Fee { amount: repeated Coin (field 1), gas_limit: uint64 (field 2) }
func EncodeFee(coins [][]byte, gasLimit uint64) []byte {
	w := NewProtoWriter()
	for _, coin := range coins {
		w.WriteMessage(1, coin)
	}
	w.WriteVarint(2, gasLimit)
	return w.Bytes()
}

// EncodeModeInfoSingle encodes ModeInfo.Single { mode: SignMode (field 1) }.
// Then wraps it in ModeInfo { single: Single (field 1) }.
func EncodeModeInfoSingle(signMode int) []byte {
	// Single { mode: enum (field 1) }
	single := NewProtoWriter()
	single.WriteVarint(1, uint64(signMode))

	// ModeInfo { single: Single (field 1) }
	mi := NewProtoWriter()
	mi.WriteMessage(1, single.Bytes())
	return mi.Bytes()
}

// EncodeSignerInfo encodes a cosmos.tx.v1beta1.SignerInfo.
// SignerInfo { public_key: Any (field 1), mode_info: ModeInfo (field 2), sequence: uint64 (field 3) }
func EncodeSignerInfo(pubKeyAny []byte, modeInfo []byte, sequence uint64) []byte {
	w := NewProtoWriter()
	w.WriteMessage(1, pubKeyAny)
	w.WriteMessage(2, modeInfo)
	w.WriteVarint(3, sequence)
	return w.Bytes()
}

// EncodeAuthInfo encodes a cosmos.tx.v1beta1.AuthInfo.
// AuthInfo { signer_infos: repeated SignerInfo (field 1), fee: Fee (field 2) }
func EncodeAuthInfo(signerInfos [][]byte, fee []byte) []byte {
	w := NewProtoWriter()
	for _, si := range signerInfos {
		w.WriteMessage(1, si)
	}
	w.WriteMessage(2, fee)
	return w.Bytes()
}

// EncodeTxBody encodes a cosmos.tx.v1beta1.TxBody.
// TxBody { messages: repeated Any (field 1), memo: string (field 2), timeout_height: uint64 (field 3) }
func EncodeTxBody(messages [][]byte, memo string, timeoutHeight uint64) []byte {
	w := NewProtoWriter()
	for _, msg := range messages {
		w.WriteMessage(1, msg)
	}
	w.WriteString(2, memo)
	w.WriteVarint(3, timeoutHeight)
	return w.Bytes()
}

// EncodeTx encodes a cosmos.tx.v1beta1.Tx.
// Tx { body: TxBody (field 1), auth_info: AuthInfo (field 2), signatures: repeated bytes (field 3) }
func EncodeTx(body []byte, authInfo []byte, signatures [][]byte) []byte {
	w := NewProtoWriter()
	w.WriteMessage(1, body)
	w.WriteMessage(2, authInfo)
	for _, sig := range signatures {
		w.WriteBytes(3, sig)
	}
	return w.Bytes()
}

// EncodeSignDoc encodes a cosmos.tx.v1beta1.SignDoc.
// SignDoc { body_bytes: bytes (field 1), auth_info_bytes: bytes (field 2),
//
//	chain_id: string (field 3), account_number: uint64 (field 4) }
func EncodeSignDoc(bodyBytes, authInfoBytes []byte, chainID string, accountNumber uint64) []byte {
	w := NewProtoWriter()
	w.WriteBytes(1, bodyBytes)
	w.WriteBytes(2, authInfoBytes)
	w.WriteString(3, chainID)
	// account_number 0 is valid and must be included
	w.WriteVarintForce(4, accountNumber)
	return w.Bytes()
}

// ============================================================================
// Cosmos SDK standard messages (bank, staking, distribution)
// ============================================================================

// EncodeMsgSend encodes cosmos.bank.v1beta1.MsgSend.
// MsgSend { from_address: string (1), to_address: string (2), amount: repeated Coin (3) }
func EncodeMsgSend(fromAddr, toAddr string, coins [][]byte) []byte {
	w := NewProtoWriter()
	w.WriteString(1, fromAddr)
	w.WriteString(2, toAddr)
	for _, coin := range coins {
		w.WriteMessage(3, coin)
	}
	return w.Bytes()
}

// EncodeMsgDelegate encodes cosmos.staking.v1beta1.MsgDelegate.
// MsgDelegate { delegator_address: string (1), validator_address: string (2), amount: Coin (3) }
func EncodeMsgDelegate(delegatorAddr, validatorAddr string, amount []byte) []byte {
	w := NewProtoWriter()
	w.WriteString(1, delegatorAddr)
	w.WriteString(2, validatorAddr)
	w.WriteMessage(3, amount)
	return w.Bytes()
}

// EncodeMsgUndelegate encodes cosmos.staking.v1beta1.MsgUndelegate.
// MsgUndelegate { delegator_address: string (1), validator_address: string (2), amount: Coin (3) }
func EncodeMsgUndelegate(delegatorAddr, validatorAddr string, amount []byte) []byte {
	w := NewProtoWriter()
	w.WriteString(1, delegatorAddr)
	w.WriteString(2, validatorAddr)
	w.WriteMessage(3, amount)
	return w.Bytes()
}

// EncodeMsgWithdrawDelegatorReward encodes cosmos.distribution.v1beta1.MsgWithdrawDelegatorReward.
// MsgWithdrawDelegatorReward { delegator_address: string (1), validator_address: string (2) }
func EncodeMsgWithdrawDelegatorReward(delegatorAddr, validatorAddr string) []byte {
	w := NewProtoWriter()
	w.WriteString(1, delegatorAddr)
	w.WriteString(2, validatorAddr)
	return w.Bytes()
}

// ============================================================================
// Application-specific messages (cc_bc POH & QA modules)
// ============================================================================

// EncodeMsgHeartbeat encodes cc_bc.poh.v1.MsgHeartbeat.
// MsgHeartbeat { miner: string (field 1) }
func EncodeMsgHeartbeat(miner string) []byte {
	w := NewProtoWriter()
	w.WriteString(1, miner)
	return w.Bytes()
}

// EncodeMsgStake encodes cc_bc.poh.v1.MsgStake.
// MsgStake { miner: string (field 1), amount: string (field 2), endpoint: string (field 3) }
func EncodeMsgStake(miner, amount, endpoint string) []byte {
	w := NewProtoWriter()
	w.WriteString(1, miner)
	w.WriteString(2, amount)
	if endpoint != "" {
		w.WriteString(3, endpoint)
	}
	return w.Bytes()
}

// EncodeMsgUnstake encodes cc_bc.poh.v1.MsgUnstake.
// MsgUnstake { miner: string (field 1) }
func EncodeMsgUnstake(miner string) []byte {
	w := NewProtoWriter()
	w.WriteString(1, miner)
	return w.Bytes()
}

// EncodeMsgSubmitQuestion encodes cc_bc.qa.v1.MsgSubmitQuestion.
// MsgSubmitQuestion { author: string (1), session_id: uint64 (2), content_hash: string (3) }
func EncodeMsgSubmitQuestion(author string, sessionID uint64, contentHash string) []byte {
	w := NewProtoWriter()
	w.WriteString(1, author)
	w.WriteVarint(2, sessionID)
	w.WriteString(3, contentHash)
	return w.Bytes()
}

// EncodeMsgSubmitAnswer encodes cc_bc.qa.v1.MsgSubmitAnswer.
// MsgSubmitAnswer { author: string (1), session_id: uint64 (2), content_hash: string (3) }
func EncodeMsgSubmitAnswer(author string, sessionID uint64, contentHash string) []byte {
	w := NewProtoWriter()
	w.WriteString(1, author)
	w.WriteVarint(2, sessionID)
	w.WriteString(3, contentHash)
	return w.Bytes()
}

// EncodeMsgCommitVote encodes cc_bc.qa.v1.MsgCommitVote.
// MsgCommitVote { voter: string (1), session_id: uint64 (2), phase: string (3), vote_hash: string (4) }
func EncodeMsgCommitVote(voter string, sessionID uint64, phase, voteHash string) []byte {
	w := NewProtoWriter()
	w.WriteString(1, voter)
	w.WriteVarint(2, sessionID)
	w.WriteString(3, phase)
	w.WriteString(4, voteHash)
	return w.Bytes()
}

// EncodeMsgRevealVote encodes cc_bc.qa.v1.MsgRevealVote.
// MsgRevealVote { voter: string (1), session_id: uint64 (2), phase: string (3), choice: string (4), salt: string (5) }
func EncodeMsgRevealVote(voter string, sessionID uint64, phase, choice, salt string) []byte {
	w := NewProtoWriter()
	w.WriteString(1, voter)
	w.WriteVarint(2, sessionID)
	w.WriteString(3, phase)
	w.WriteString(4, choice)
	w.WriteString(5, salt)
	return w.Bytes()
}
