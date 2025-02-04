package main

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/fatih/color"
	"github.com/ipfs/go-cid"
	cbor "github.com/ipfs/go-ipld-cbor"
	"github.com/libp2p/go-libp2p-core/peer"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/urfave/cli/v2"
	"golang.org/x/xerrors"

	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-bitfield"
	rlepluslazy "github.com/filecoin-project/go-bitfield/rle"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/go-state-types/big"
	builtint "github.com/filecoin-project/go-state-types/builtin"
	"github.com/filecoin-project/go-state-types/builtin/v8/miner"
	"github.com/filecoin-project/go-state-types/network"
	msig2 "github.com/filecoin-project/specs-actors/v2/actors/builtin/multisig"

	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/blockstore"
	"github.com/filecoin-project/lotus/build"
	"github.com/filecoin-project/lotus/chain/actors"
	"github.com/filecoin-project/lotus/chain/actors/adt"
	"github.com/filecoin-project/lotus/chain/actors/builtin"
	lminer "github.com/filecoin-project/lotus/chain/actors/builtin/miner"
	"github.com/filecoin-project/lotus/chain/types"
	lcli "github.com/filecoin-project/lotus/cli"
	"github.com/filecoin-project/lotus/lib/tablewriter"
)

var actorCmd = &cli.Command{
	Name:  "actor",
	Usage: "manipulate the miner actor",
	Subcommands: []*cli.Command{
		actorSetAddrsCmd,
		actorWithdrawCmd,
		actorRepayDebtCmd,
		actorSetPeeridCmd,
		actorSetOwnerCmd,
		actorControl,
		actorProposeChangeWorker,
		actorConfirmChangeWorker,
		actorCompactAllocatedCmd,
	},
}

var actorSetAddrsCmd = &cli.Command{
	Name:    "set-addresses",
	Aliases: []string{"set-addrs"},
	Usage:   "set addresses that your miner can be publicly dialed on",
	Flags: []cli.Flag{
		&cli.Int64Flag{
			Name:  "gas-limit",
			Usage: "set gas limit",
			Value: 0,
		},
		&cli.BoolFlag{
			Name:  "unset",
			Usage: "unset address",
			Value: false,
		},
	},
	Action: func(cctx *cli.Context) error {
		args := cctx.Args().Slice()
		unset := cctx.Bool("unset")
		if len(args) == 0 && !unset {
			return cli.ShowSubcommandHelp(cctx)
		}
		if len(args) > 0 && unset {
			return fmt.Errorf("unset can only be used with no arguments")
		}

		nodeAPI, closer, err := lcli.GetStorageMinerAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()

		api, acloser, err := lcli.GetFullNodeAPI(cctx)
		if err != nil {
			return err
		}
		defer acloser()

		ctx := lcli.ReqContext(cctx)

		var addrs []abi.Multiaddrs
		for _, a := range args {
			maddr, err := ma.NewMultiaddr(a)
			if err != nil {
				return fmt.Errorf("failed to parse %q as a multiaddr: %w", a, err)
			}

			maddrNop2p, strip := ma.SplitFunc(maddr, func(c ma.Component) bool {
				return c.Protocol().Code == ma.P_P2P
			})

			if strip != nil {
				fmt.Println("Stripping peerid ", strip, " from ", maddr)
			}
			addrs = append(addrs, maddrNop2p.Bytes())
		}

		maddr, err := nodeAPI.ActorAddress(ctx)
		if err != nil {
			return err
		}

		minfo, err := api.StateMinerInfo(ctx, maddr, types.EmptyTSK)
		if err != nil {
			return err
		}

		params, err := actors.SerializeParams(&miner.ChangeMultiaddrsParams{NewMultiaddrs: addrs})
		if err != nil {
			return err
		}

		gasLimit := cctx.Int64("gas-limit")

		smsg, err := api.MpoolPushMessage(ctx, &types.Message{
			To:       maddr,
			From:     minfo.Worker,
			Value:    types.NewInt(0),
			GasLimit: gasLimit,
			Method:   builtint.MethodsMiner.ChangeMultiaddrs,
			Params:   params,
		}, nil)
		if err != nil {
			return err
		}

		fmt.Printf("Requested multiaddrs change in message %s\n", smsg.Cid())
		return nil

	},
}
var actorSetPeeridCmd = &cli.Command{
	Name:  "set-peer-id",
	Usage: "set the peer id of your miner",
	Flags: []cli.Flag{
		&cli.Int64Flag{
			Name:  "gas-limit",
			Usage: "set gas limit",
			Value: 0,
		},
	},
	Action: func(cctx *cli.Context) error {
		nodeAPI, closer, err := lcli.GetStorageMinerAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()

		api, acloser, err := lcli.GetFullNodeAPI(cctx)
		if err != nil {
			return err
		}
		defer acloser()

		ctx := lcli.ReqContext(cctx)

		pid, err := peer.Decode(cctx.Args().Get(0))
		if err != nil {
			return fmt.Errorf("failed to parse input as a peerId: %w", err)
		}

		maddr, err := nodeAPI.ActorAddress(ctx)
		if err != nil {
			return err
		}

		minfo, err := api.StateMinerInfo(ctx, maddr, types.EmptyTSK)
		if err != nil {
			return err
		}

		params, err := actors.SerializeParams(&miner.ChangePeerIDParams{NewID: abi.PeerID(pid)})
		if err != nil {
			return err
		}

		gasLimit := cctx.Int64("gas-limit")

		smsg, err := api.MpoolPushMessage(ctx, &types.Message{
			To:       maddr,
			From:     minfo.Worker,
			Value:    types.NewInt(0),
			GasLimit: gasLimit,
			Method:   builtint.MethodsMiner.ChangePeerID,
			Params:   params,
		}, nil)
		if err != nil {
			return err
		}

		fmt.Printf("Requested peerid change in message %s\n", smsg.Cid())
		return nil

	},
}

var actorWithdrawMsigProposeCmd = &cli.Command{
	Name:      "propose",
	Usage:     "Propose a multisig transaction",
	ArgsUsage: "[multisigAddress destinationAddress value <methodId methodParams> (optional)]",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "encoding",
			Value: "hex",
			Usage: "specify params encoding to parse (base64, hex)",
		},
		&cli.StringFlag{
			Name:  "from",
			Usage: "account to send the propose message from",
		},
	},
	Action: func(cctx *cli.Context) error {
		if cctx.Args().Len() < 3 {
			return lcli.ShowHelp(cctx, fmt.Errorf("must pass at least multisig address, destination, and value"))
		}

		if cctx.Args().Len() > 3 && cctx.Args().Len() != 5 {
			return lcli.ShowHelp(cctx, fmt.Errorf("must either pass three or five arguments"))
		}

		srv, err := lcli.GetFullNodeServices(cctx)
		if err != nil {
			return err
		}
		defer srv.Close() //nolint:errcheck

		api := srv.FullNodeAPI()
		ctx := lcli.ReqContext(cctx)

		msig, err := address.NewFromString(cctx.Args().Get(0))
		if err != nil {
			return err
		}

		dest, err := address.NewFromString(cctx.Args().Get(1))
		if err != nil {
			return err
		}

		value, err := types.ParseFIL(cctx.Args().Get(2))
		if err != nil {
			return err
		}

		available, err := api.StateMinerAvailableBalance(ctx, msig, types.EmptyTSK)
		if err != nil {
			return err
		}

		var method uint64
		var params []byte
		if cctx.Args().Len() == 5 {
			m, err := strconv.ParseUint(cctx.Args().Get(3), 10, 64)
			if err != nil {
				return err
			}
			method = m

			// get amount
			amount := available
			if cctx.Args().Present() {
				f, err := types.ParseFIL(cctx.Args().Get(4))
				if err != nil {
					return xerrors.Errorf("parsing 'amount' argument: %w", err)
				}

				amount = abi.TokenAmount(f)

				if amount.GreaterThan(available) {
					return xerrors.Errorf("can't withdraw more funds than available; requested: %s; available: %s", types.FIL(amount), types.FIL(available))
				}
			}

			params, err = actors.SerializeParams(&miner.WithdrawBalanceParams{
				AmountRequested: amount, // Default to attempting to withdraw all the extra funds in the miner actor
			})
			if err != nil {
				return err
			}

		}

		var from address.Address
		if cctx.IsSet("from") {
			f, err := address.NewFromString(cctx.String("from"))
			if err != nil {
				return err
			}
			from = f
		} else {
			defaddr, err := api.WalletDefaultAddress(ctx)
			if err != nil {
				return err
			}
			from = defaddr
		}

		act, err := api.StateGetActor(ctx, msig, types.EmptyTSK)
		if err != nil {
			return fmt.Errorf("failed to look up multisig %s: %w", msig, err)
		}

		if !builtin.IsMultisigActor(act.Code) {
			return fmt.Errorf("actor %s is not a multisig actor", msig)
		}

		proto, err := api.MsigPropose(ctx, msig, dest, types.BigInt(value), from, method, params)
		if err != nil {
			return err
		}

		sm, err := lcli.InteractiveSend(ctx, cctx, srv, proto)
		if err != nil {
			return err
		}

		msgCid := sm.Cid()

		fmt.Println("sent proposal in message: ", msgCid)

		wait, err := api.StateWaitMsg(ctx, msgCid, uint64(cctx.Int("confidence")), build.Finality, true)
		if err != nil {
			return err
		}

		if wait.Receipt.ExitCode != 0 {
			return fmt.Errorf("proposal returned exit %d", wait.Receipt.ExitCode)
		}

		var retval msig2.ProposeReturn
		if err := retval.UnmarshalCBOR(bytes.NewReader(wait.Receipt.Return)); err != nil {
			return fmt.Errorf("failed to unmarshal propose return value: %w", err)
		}

		fmt.Printf("Transaction ID: %d\n", retval.TxnID)
		if retval.Applied {
			fmt.Printf("Transaction was executed during propose\n")
			fmt.Printf("Exit Code: %d\n", retval.Code)
			fmt.Printf("Return Value: %x\n", retval.Ret)
		}

		return nil
	},
}
var actorWithdrawMsigApproveCmd = &cli.Command{
	Name:      "approve",
	Usage:     "Approve a multisig message",
	ArgsUsage: "<multisigAddress messageId> [proposerAddress destination value [methodId methodParams]]",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "from",
			Usage: "account to send the approve message from",
		},
	},
	Action: func(cctx *cli.Context) error {

		// pass to msigCommand
		if cctx.Args().Len() < 2 {
			return lcli.ShowHelp(cctx, fmt.Errorf("must pass at least multisig address and message ID"))
		}

		if cctx.Args().Len() > 2 && cctx.Args().Len() < 5 {
			return lcli.ShowHelp(cctx, fmt.Errorf("usage: msig approve <msig addr> <message ID> <proposer address> <desination> <value>"))
		}

		if cctx.Args().Len() > 5 && cctx.Args().Len() != 7 {
			return lcli.ShowHelp(cctx, fmt.Errorf("usage: msig approve <msig addr> <message ID> <proposer address> <desination> <value> [ <method> <params> ]"))
		}

		srv, err := lcli.GetFullNodeServices(cctx)
		if err != nil {
			return err
		}
		defer srv.Close() //nolint:errcheck

		api := srv.FullNodeAPI()
		ctx := lcli.ReqContext(cctx)

		msig, err := address.NewFromString(cctx.Args().Get(0))
		if err != nil {
			return err
		}

		txid, err := strconv.ParseUint(cctx.Args().Get(1), 10, 64)
		if err != nil {
			return err
		}

		var from address.Address
		if cctx.IsSet("from") {
			f, err := address.NewFromString(cctx.String("from"))
			if err != nil {
				return err
			}
			from = f
		} else {
			defaddr, err := api.WalletDefaultAddress(ctx)
			if err != nil {
				return err
			}
			from = defaddr
		}

		var msgCid cid.Cid
		if cctx.Args().Len() == 2 {
			proto, err := api.MsigApprove(ctx, msig, txid, from)
			if err != nil {
				return err
			}

			sm, err := lcli.InteractiveSend(ctx, cctx, srv, proto)
			if err != nil {
				return err
			}

			msgCid = sm.Cid()
		} else {
			proposer, err := address.NewFromString(cctx.Args().Get(2))
			if err != nil {
				return err
			}

			if proposer.Protocol() != address.ID {
				proposer, err = api.StateLookupID(ctx, proposer, types.EmptyTSK)
				if err != nil {
					return err
				}
			}

			dest, err := address.NewFromString(cctx.Args().Get(3))
			if err != nil {
				return err
			}

			var method uint64
			var params []byte
			var paramsStr string
			if cctx.Args().Len() == 7 {
				m, err := strconv.ParseUint(cctx.Args().Get(5), 10, 64)
				if err != nil {
					return err
				}
				method = m

				p, err := srv.DecodeTypedParamsFromJSON(ctx, dest, abi.MethodNum(method), cctx.Args().Get(6))
				if err != nil {
					return xerrors.Errorf("decoding json params: %w", err)
				}

				switch cctx.String("encoding") {
				case "base64", "b64":
					paramsStr = base64.StdEncoding.EncodeToString(p)
				case "hex":
					paramsStr = hex.EncodeToString(p)
				default:
					return xerrors.Errorf("unknown encoding")
				}

				params = []byte(paramsStr)
			}

			proto, err := api.MsigApproveTxnHash(ctx, msig, txid, proposer, dest, big.Zero(), from, uint64(builtint.MethodsMiner.WithdrawBalance), params)
			if err != nil {
				return err
			}

			sm, err := lcli.InteractiveSend(ctx, cctx, srv, proto)
			if err != nil {
				return err
			}

			msgCid = sm.Cid()
		}

		fmt.Println("sent approval in message: ", msgCid)

		wait, err := api.StateWaitMsg(ctx, msgCid, uint64(cctx.Int("confidence")), build.Finality, true)
		if err != nil {
			return err
		}

		if wait.Receipt.ExitCode != 0 {
			return fmt.Errorf("approve returned exit %d", wait.Receipt.ExitCode)
		}

		return nil
	},
}

var actorWithdrawCmd = &cli.Command{
	Name:      "withdraw",
	Usage:     "withdraw available balance",
	ArgsUsage: "[amount (FIL)]",
	Subcommands: []*cli.Command{
		actorWithdrawMsigProposeCmd,
		actorWithdrawMsigApproveCmd,
	},
	Flags: []cli.Flag{
		&cli.IntFlag{
			Name:  "confidence",
			Usage: "number of block confirmations to wait for",
			Value: int(build.MessageConfidence),
		},
	},
	Action: func(cctx *cli.Context) error {
		nodeApi, closer, err := lcli.GetStorageMinerAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()

		api, acloser, err := lcli.GetFullNodeAPI(cctx)
		if err != nil {
			return err
		}
		defer acloser()

		ctx := lcli.ReqContext(cctx)

		maddr, err := nodeApi.ActorAddress(ctx)
		if err != nil {
			return err
		}

		mi, err := api.StateMinerInfo(ctx, maddr, types.EmptyTSK)
		if err != nil {
			return err
		}

		available, err := api.StateMinerAvailableBalance(ctx, maddr, types.EmptyTSK)
		if err != nil {
			return err
		}

		amount := available
		if cctx.Args().Present() {
			f, err := types.ParseFIL(cctx.Args().First())
			if err != nil {
				return xerrors.Errorf("parsing 'amount' argument: %w", err)
			}

			amount = abi.TokenAmount(f)

			if amount.GreaterThan(available) {
				return xerrors.Errorf("can't withdraw more funds than available; requested: %s; available: %s", types.FIL(amount), types.FIL(available))
			}
		}

		params, err := actors.SerializeParams(&miner.WithdrawBalanceParams{
			AmountRequested: amount, // Default to attempting to withdraw all the extra funds in the miner actor
		})
		if err != nil {
			return err
		}

		smsg, err := api.MpoolPushMessage(ctx, &types.Message{
			To:     maddr,
			From:   mi.Owner,
			Value:  types.NewInt(0),
			Method: builtint.MethodsMiner.WithdrawBalance,
			Params: params,
		}, nil)
		if err != nil {
			return err
		}

		fmt.Printf("Requested rewards withdrawal in message %s\n", smsg.Cid())

		// wait for it to get mined into a block
		fmt.Printf("waiting for %d epochs for confirmation..\n", uint64(cctx.Int("confidence")))

		wait, err := api.StateWaitMsg(ctx, smsg.Cid(), uint64(cctx.Int("confidence")))
		if err != nil {
			return err
		}

		// check it executed successfully
		if wait.Receipt.ExitCode != 0 {
			fmt.Println(cctx.App.Writer, "withdrawal failed!")
			return err
		}

		nv, err := api.StateNetworkVersion(ctx, wait.TipSet)
		if err != nil {
			return err
		}

		if nv >= network.Version14 {
			var withdrawn abi.TokenAmount
			if err := withdrawn.UnmarshalCBOR(bytes.NewReader(wait.Receipt.Return)); err != nil {
				return err
			}

			fmt.Printf("Successfully withdrew %s \n", types.FIL(withdrawn))
			if withdrawn.LessThan(amount) {
				fmt.Printf("Note that this is less than the requested amount of %s\n", types.FIL(amount))
			}
		}

		return nil
	},
}

var actorRepayDebtCmd = &cli.Command{
	Name:      "repay-debt",
	Usage:     "pay down a miner's debt",
	ArgsUsage: "[amount (FIL)]",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "from",
			Usage: "optionally specify the account to send funds from",
		},
	},
	Action: func(cctx *cli.Context) error {
		nodeApi, closer, err := lcli.GetStorageMinerAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()

		api, acloser, err := lcli.GetFullNodeAPI(cctx)
		if err != nil {
			return err
		}
		defer acloser()

		ctx := lcli.ReqContext(cctx)

		maddr, err := nodeApi.ActorAddress(ctx)
		if err != nil {
			return err
		}

		mi, err := api.StateMinerInfo(ctx, maddr, types.EmptyTSK)
		if err != nil {
			return err
		}

		var amount abi.TokenAmount
		if cctx.Args().Present() {
			f, err := types.ParseFIL(cctx.Args().First())
			if err != nil {
				return xerrors.Errorf("parsing 'amount' argument: %w", err)
			}

			amount = abi.TokenAmount(f)
		} else {
			mact, err := api.StateGetActor(ctx, maddr, types.EmptyTSK)
			if err != nil {
				return err
			}

			store := adt.WrapStore(ctx, cbor.NewCborStore(blockstore.NewAPIBlockstore(api)))

			mst, err := lminer.Load(store, mact)
			if err != nil {
				return err
			}

			amount, err = mst.FeeDebt()
			if err != nil {
				return err
			}

		}

		fromAddr := mi.Worker
		if from := cctx.String("from"); from != "" {
			addr, err := address.NewFromString(from)
			if err != nil {
				return err
			}

			fromAddr = addr
		}

		fromId, err := api.StateLookupID(ctx, fromAddr, types.EmptyTSK)
		if err != nil {
			return err
		}

		if !isController(mi, fromId) {
			return xerrors.Errorf("sender isn't a controller of miner: %s", fromId)
		}

		smsg, err := api.MpoolPushMessage(ctx, &types.Message{
			To:     maddr,
			From:   fromId,
			Value:  amount,
			Method: builtint.MethodsMiner.RepayDebt,
			Params: nil,
		}, nil)
		if err != nil {
			return err
		}

		fmt.Printf("Sent repay debt message %s\n", smsg.Cid())

		return nil
	},
}

var actorControl = &cli.Command{
	Name:  "control",
	Usage: "Manage control addresses",
	Subcommands: []*cli.Command{
		actorControlList,
		actorControlSet,
	},
}

var actorControlList = &cli.Command{
	Name:  "list",
	Usage: "Get currently set control addresses",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name: "verbose",
		},
		&cli.BoolFlag{
			Name:        "color",
			Usage:       "use color in display output",
			DefaultText: "depends on output being a TTY",
		},
	},
	Action: func(cctx *cli.Context) error {
		if cctx.IsSet("color") {
			color.NoColor = !cctx.Bool("color")
		}

		nodeApi, closer, err := lcli.GetStorageMinerAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()

		api, acloser, err := lcli.GetFullNodeAPI(cctx)
		if err != nil {
			return err
		}
		defer acloser()

		ctx := lcli.ReqContext(cctx)

		maddr, err := getActorAddress(ctx, cctx)
		if err != nil {
			return err
		}

		mi, err := api.StateMinerInfo(ctx, maddr, types.EmptyTSK)
		if err != nil {
			return err
		}

		tw := tablewriter.New(
			tablewriter.Col("name"),
			tablewriter.Col("ID"),
			tablewriter.Col("key"),
			tablewriter.Col("use"),
			tablewriter.Col("balance"),
		)

		ac, err := nodeApi.ActorAddressConfig(ctx)
		if err != nil {
			return err
		}

		commit := map[address.Address]struct{}{}
		precommit := map[address.Address]struct{}{}
		terminate := map[address.Address]struct{}{}
		dealPublish := map[address.Address]struct{}{}
		post := map[address.Address]struct{}{}

		for _, ca := range mi.ControlAddresses {
			post[ca] = struct{}{}
		}

		for _, ca := range ac.PreCommitControl {
			ca, err := api.StateLookupID(ctx, ca, types.EmptyTSK)
			if err != nil {
				return err
			}

			delete(post, ca)
			precommit[ca] = struct{}{}
		}

		for _, ca := range ac.CommitControl {
			ca, err := api.StateLookupID(ctx, ca, types.EmptyTSK)
			if err != nil {
				return err
			}

			delete(post, ca)
			commit[ca] = struct{}{}
		}

		for _, ca := range ac.TerminateControl {
			ca, err := api.StateLookupID(ctx, ca, types.EmptyTSK)
			if err != nil {
				return err
			}

			delete(post, ca)
			terminate[ca] = struct{}{}
		}

		for _, ca := range ac.DealPublishControl {
			ca, err := api.StateLookupID(ctx, ca, types.EmptyTSK)
			if err != nil {
				return err
			}

			delete(post, ca)
			dealPublish[ca] = struct{}{}
		}

		printKey := func(name string, a address.Address) {
			b, err := api.WalletBalance(ctx, a)
			if err != nil {
				fmt.Printf("%s\t%s: error getting balance: %s\n", name, a, err)
				return
			}

			k, err := api.StateAccountKey(ctx, a, types.EmptyTSK)
			if err != nil {
				fmt.Printf("%s\t%s: error getting account key: %s\n", name, a, err)
				return
			}

			kstr := k.String()
			if !cctx.Bool("verbose") {
				kstr = kstr[:9] + "..."
			}

			bstr := types.FIL(b).String()
			switch {
			case b.LessThan(types.FromFil(10)):
				bstr = color.RedString(bstr)
			case b.LessThan(types.FromFil(50)):
				bstr = color.YellowString(bstr)
			default:
				bstr = color.GreenString(bstr)
			}

			var uses []string
			if a == mi.Worker {
				uses = append(uses, color.YellowString("other"))
			}
			if _, ok := post[a]; ok {
				uses = append(uses, color.GreenString("post"))
			}
			if _, ok := precommit[a]; ok {
				uses = append(uses, color.CyanString("precommit"))
			}
			if _, ok := commit[a]; ok {
				uses = append(uses, color.BlueString("commit"))
			}
			if _, ok := terminate[a]; ok {
				uses = append(uses, color.YellowString("terminate"))
			}
			if _, ok := dealPublish[a]; ok {
				uses = append(uses, color.MagentaString("deals"))
			}

			tw.Write(map[string]interface{}{
				"name":    name,
				"ID":      a,
				"key":     kstr,
				"use":     strings.Join(uses, " "),
				"balance": bstr,
			})
		}

		printKey("owner", mi.Owner)
		printKey("worker", mi.Worker)
		for i, ca := range mi.ControlAddresses {
			printKey(fmt.Sprintf("control-%d", i), ca)
		}

		return tw.Flush(os.Stdout)
	},
}

var actorControlSet = &cli.Command{
	Name:      "set",
	Usage:     "Set control address(-es)",
	ArgsUsage: "[...address]",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "really-do-it",
			Usage: "Actually send transaction performing the action",
			Value: false,
		},
	},
	Action: func(cctx *cli.Context) error {
		nodeApi, closer, err := lcli.GetStorageMinerAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()

		api, acloser, err := lcli.GetFullNodeAPI(cctx)
		if err != nil {
			return err
		}
		defer acloser()

		ctx := lcli.ReqContext(cctx)

		maddr, err := nodeApi.ActorAddress(ctx)
		if err != nil {
			return err
		}

		mi, err := api.StateMinerInfo(ctx, maddr, types.EmptyTSK)
		if err != nil {
			return err
		}

		del := map[address.Address]struct{}{}
		existing := map[address.Address]struct{}{}
		for _, controlAddress := range mi.ControlAddresses {
			ka, err := api.StateAccountKey(ctx, controlAddress, types.EmptyTSK)
			if err != nil {
				return err
			}

			del[ka] = struct{}{}
			existing[ka] = struct{}{}
		}

		var toSet []address.Address

		for i, as := range cctx.Args().Slice() {
			a, err := address.NewFromString(as)
			if err != nil {
				return xerrors.Errorf("parsing address %d: %w", i, err)
			}

			ka, err := api.StateAccountKey(ctx, a, types.EmptyTSK)
			if err != nil {
				return err
			}

			// make sure the address exists on chain
			_, err = api.StateLookupID(ctx, ka, types.EmptyTSK)
			if err != nil {
				return xerrors.Errorf("looking up %s: %w", ka, err)
			}

			delete(del, ka)
			toSet = append(toSet, ka)
		}

		for a := range del {
			fmt.Println("Remove", a)
		}
		for _, a := range toSet {
			if _, exists := existing[a]; !exists {
				fmt.Println("Add", a)
			}
		}

		if !cctx.Bool("really-do-it") {
			fmt.Println("Pass --really-do-it to actually execute this action")
			return nil
		}

		cwp := &miner.ChangeWorkerAddressParams{
			NewWorker:       mi.Worker,
			NewControlAddrs: toSet,
		}

		sp, err := actors.SerializeParams(cwp)
		if err != nil {
			return xerrors.Errorf("serializing params: %w", err)
		}

		smsg, err := api.MpoolPushMessage(ctx, &types.Message{
			From:   mi.Owner,
			To:     maddr,
			Method: builtint.MethodsMiner.ChangeWorkerAddress,

			Value:  big.Zero(),
			Params: sp,
		}, nil)
		if err != nil {
			return xerrors.Errorf("mpool push: %w", err)
		}

		fmt.Println("Message CID:", smsg.Cid())

		return nil
	},
}

var actorSetOwnerCmd = &cli.Command{
	Name:      "set-owner",
	Usage:     "Set owner address (this command should be invoked twice, first with the old owner as the senderAddress, and then with the new owner)",
	ArgsUsage: "[newOwnerAddress senderAddress]",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "really-do-it",
			Usage: "Actually send transaction performing the action",
			Value: false,
		},
	},
	Action: func(cctx *cli.Context) error {
		if !cctx.Bool("really-do-it") {
			fmt.Println("Pass --really-do-it to actually execute this action")
			return nil
		}

		if cctx.NArg() != 2 {
			return fmt.Errorf("must pass new owner address and sender address")
		}

		api, acloser, err := lcli.GetFullNodeAPI(cctx)
		if err != nil {
			return err
		}
		defer acloser()

		ctx := lcli.ReqContext(cctx)

		na, err := address.NewFromString(cctx.Args().First())
		if err != nil {
			return err
		}

		newAddrId, err := api.StateLookupID(ctx, na, types.EmptyTSK)
		if err != nil {
			return err
		}

		fa, err := address.NewFromString(cctx.Args().Get(1))
		if err != nil {
			return err
		}

		fromAddrId, err := api.StateLookupID(ctx, fa, types.EmptyTSK)
		if err != nil {
			return err
		}

		maddr, err := getActorAddress(ctx, cctx)
		if err != nil {
			return err
		}

		mi, err := api.StateMinerInfo(ctx, maddr, types.EmptyTSK)
		if err != nil {
			return err
		}

		if fromAddrId != mi.Owner && fromAddrId != newAddrId {
			return xerrors.New("from address must either be the old owner or the new owner")
		}

		sp, err := actors.SerializeParams(&newAddrId)
		if err != nil {
			return xerrors.Errorf("serializing params: %w", err)
		}

		smsg, err := api.MpoolPushMessage(ctx, &types.Message{
			From:   fromAddrId,
			To:     maddr,
			Method: builtint.MethodsMiner.ChangeOwnerAddress,
			Value:  big.Zero(),
			Params: sp,
		}, nil)
		if err != nil {
			return xerrors.Errorf("mpool push: %w", err)
		}

		fmt.Println("Message CID:", smsg.Cid())

		// wait for it to get mined into a block
		wait, err := api.StateWaitMsg(ctx, smsg.Cid(), build.MessageConfidence)
		if err != nil {
			return err
		}

		// check it executed successfully
		if wait.Receipt.ExitCode != 0 {
			fmt.Println("owner change failed!")
			return err
		}

		fmt.Println("message succeeded!")

		return nil
	},
}

var actorProposeChangeWorker = &cli.Command{
	Name:      "propose-change-worker",
	Usage:     "Propose a worker address change",
	ArgsUsage: "[address]",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "really-do-it",
			Usage: "Actually send transaction performing the action",
			Value: false,
		},
	},
	Action: func(cctx *cli.Context) error {
		if !cctx.Args().Present() {
			return fmt.Errorf("must pass address of new worker address")
		}

		nodeApi, closer, err := lcli.GetStorageMinerAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()

		api, acloser, err := lcli.GetFullNodeAPI(cctx)
		if err != nil {
			return err
		}
		defer acloser()

		ctx := lcli.ReqContext(cctx)

		na, err := address.NewFromString(cctx.Args().First())
		if err != nil {
			return err
		}

		newAddr, err := api.StateLookupID(ctx, na, types.EmptyTSK)
		if err != nil {
			return err
		}

		maddr, err := nodeApi.ActorAddress(ctx)
		if err != nil {
			return err
		}

		mi, err := api.StateMinerInfo(ctx, maddr, types.EmptyTSK)
		if err != nil {
			return err
		}

		if mi.NewWorker.Empty() {
			if mi.Worker == newAddr {
				return fmt.Errorf("worker address already set to %s", na)
			}
		} else {
			if mi.NewWorker == newAddr {
				return fmt.Errorf("change to worker address %s already pending", na)
			}
		}

		if !cctx.Bool("really-do-it") {
			fmt.Fprintln(cctx.App.Writer, "Pass --really-do-it to actually execute this action")
			return nil
		}

		cwp := &miner.ChangeWorkerAddressParams{
			NewWorker:       newAddr,
			NewControlAddrs: mi.ControlAddresses,
		}

		sp, err := actors.SerializeParams(cwp)
		if err != nil {
			return xerrors.Errorf("serializing params: %w", err)
		}

		smsg, err := api.MpoolPushMessage(ctx, &types.Message{
			From:   mi.Owner,
			To:     maddr,
			Method: builtint.MethodsMiner.ChangeWorkerAddress,
			Value:  big.Zero(),
			Params: sp,
		}, nil)
		if err != nil {
			return xerrors.Errorf("mpool push: %w", err)
		}

		fmt.Fprintln(cctx.App.Writer, "Propose Message CID:", smsg.Cid())

		// wait for it to get mined into a block
		wait, err := api.StateWaitMsg(ctx, smsg.Cid(), build.MessageConfidence)
		if err != nil {
			return err
		}

		// check it executed successfully
		if wait.Receipt.ExitCode != 0 {
			fmt.Fprintln(cctx.App.Writer, "Propose worker change failed!")
			return err
		}

		mi, err = api.StateMinerInfo(ctx, maddr, wait.TipSet)
		if err != nil {
			return err
		}
		if mi.NewWorker != newAddr {
			return fmt.Errorf("Proposed worker address change not reflected on chain: expected '%s', found '%s'", na, mi.NewWorker)
		}

		fmt.Fprintf(cctx.App.Writer, "Worker key change to %s successfully sent, change happens at height %d.\n", na, mi.WorkerChangeEpoch)
		fmt.Fprintf(cctx.App.Writer, "If you have no active deadlines, call 'confirm-change-worker' at or after height %d to complete.\n", mi.WorkerChangeEpoch)

		return nil
	},
}

var actorConfirmChangeWorker = &cli.Command{
	Name:      "confirm-change-worker",
	Usage:     "Confirm a worker address change",
	ArgsUsage: "[address]",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "really-do-it",
			Usage: "Actually send transaction performing the action",
			Value: false,
		},
	},
	Action: func(cctx *cli.Context) error {
		if !cctx.Args().Present() {
			return fmt.Errorf("must pass address of new worker address")
		}

		nodeApi, closer, err := lcli.GetStorageMinerAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()

		api, acloser, err := lcli.GetFullNodeAPI(cctx)
		if err != nil {
			return err
		}
		defer acloser()

		ctx := lcli.ReqContext(cctx)

		na, err := address.NewFromString(cctx.Args().First())
		if err != nil {
			return err
		}

		newAddr, err := api.StateLookupID(ctx, na, types.EmptyTSK)
		if err != nil {
			return err
		}

		maddr, err := nodeApi.ActorAddress(ctx)
		if err != nil {
			return err
		}

		mi, err := api.StateMinerInfo(ctx, maddr, types.EmptyTSK)
		if err != nil {
			return err
		}

		if mi.NewWorker.Empty() {
			return xerrors.Errorf("no worker key change proposed")
		} else if mi.NewWorker != newAddr {
			return xerrors.Errorf("worker key %s does not match current worker key proposal %s", newAddr, mi.NewWorker)
		}

		if head, err := api.ChainHead(ctx); err != nil {
			return xerrors.Errorf("failed to get the chain head: %w", err)
		} else if head.Height() < mi.WorkerChangeEpoch {
			return xerrors.Errorf("worker key change cannot be confirmed until %d, current height is %d", mi.WorkerChangeEpoch, head.Height())
		}

		if !cctx.Bool("really-do-it") {
			fmt.Fprintln(cctx.App.Writer, "Pass --really-do-it to actually execute this action")
			return nil
		}

		smsg, err := api.MpoolPushMessage(ctx, &types.Message{
			From:   mi.Owner,
			To:     maddr,
			Method: builtint.MethodsMiner.ConfirmUpdateWorkerKey,
			Value:  big.Zero(),
		}, nil)
		if err != nil {
			return xerrors.Errorf("mpool push: %w", err)
		}

		fmt.Fprintln(cctx.App.Writer, "Confirm Message CID:", smsg.Cid())

		// wait for it to get mined into a block
		wait, err := api.StateWaitMsg(ctx, smsg.Cid(), build.MessageConfidence)
		if err != nil {
			return err
		}

		// check it executed successfully
		if wait.Receipt.ExitCode != 0 {
			fmt.Fprintln(cctx.App.Writer, "Worker change failed!")
			return err
		}

		mi, err = api.StateMinerInfo(ctx, maddr, wait.TipSet)
		if err != nil {
			return err
		}
		if mi.Worker != newAddr {
			return fmt.Errorf("Confirmed worker address change not reflected on chain: expected '%s', found '%s'", newAddr, mi.Worker)
		}

		return nil
	},
}

var actorCompactAllocatedCmd = &cli.Command{
	Name:  "compact-allocated",
	Usage: "compact allocated sectors bitfield",
	Flags: []cli.Flag{
		&cli.Uint64Flag{
			Name:  "mask-last-offset",
			Usage: "Mask sector IDs from 0 to 'higest_allocated - offset'",
		},
		&cli.Uint64Flag{
			Name:  "mask-upto-n",
			Usage: "Mask sector IDs from 0 to 'n'",
		},
		&cli.BoolFlag{
			Name:  "really-do-it",
			Usage: "Actually send transaction performing the action",
			Value: false,
		},
	},
	Action: func(cctx *cli.Context) error {
		if !cctx.Bool("really-do-it") {
			fmt.Println("Pass --really-do-it to actually execute this action")
			return nil
		}

		if !cctx.Args().Present() {
			return fmt.Errorf("must pass address of new owner address")
		}

		nodeApi, closer, err := lcli.GetStorageMinerAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()

		api, acloser, err := lcli.GetFullNodeAPI(cctx)
		if err != nil {
			return err
		}
		defer acloser()

		ctx := lcli.ReqContext(cctx)

		maddr, err := nodeApi.ActorAddress(ctx)
		if err != nil {
			return err
		}

		mact, err := api.StateGetActor(ctx, maddr, types.EmptyTSK)
		if err != nil {
			return err
		}

		store := adt.WrapStore(ctx, cbor.NewCborStore(blockstore.NewAPIBlockstore(api)))

		mst, err := lminer.Load(store, mact)
		if err != nil {
			return err
		}

		allocs, err := mst.GetAllocatedSectors()
		if err != nil {
			return err
		}

		var maskBf bitfield.BitField

		{
			exclusiveFlags := []string{"mask-last-offset", "mask-upto-n"}
			hasFlag := false
			for _, f := range exclusiveFlags {
				if hasFlag && cctx.IsSet(f) {
					return xerrors.Errorf("more than one 'mask` flag set")
				}
				hasFlag = hasFlag || cctx.IsSet(f)
			}
		}
		switch {
		case cctx.IsSet("mask-last-offset"):
			last, err := allocs.Last()
			if err != nil {
				return err
			}

			m := cctx.Uint64("mask-last-offset")
			if last <= m+1 {
				return xerrors.Errorf("highest allocated sector lower than mask offset %d: %d", m+1, last)
			}
			// securty to not brick a miner
			if last > 1<<60 {
				return xerrors.Errorf("very high last sector number, refusing to mask: %d", last)
			}

			maskBf, err = bitfield.NewFromIter(&rlepluslazy.RunSliceIterator{
				Runs: []rlepluslazy.Run{{Val: true, Len: last - m}}})
			if err != nil {
				return xerrors.Errorf("forming bitfield: %w", err)
			}
		case cctx.IsSet("mask-upto-n"):
			n := cctx.Uint64("mask-upto-n")
			maskBf, err = bitfield.NewFromIter(&rlepluslazy.RunSliceIterator{
				Runs: []rlepluslazy.Run{{Val: true, Len: n}}})
			if err != nil {
				return xerrors.Errorf("forming bitfield: %w", err)
			}
		default:
			return xerrors.Errorf("no 'mask' flags set")
		}

		mi, err := api.StateMinerInfo(ctx, maddr, types.EmptyTSK)
		if err != nil {
			return err
		}

		params := &miner.CompactSectorNumbersParams{
			MaskSectorNumbers: maskBf,
		}

		sp, err := actors.SerializeParams(params)
		if err != nil {
			return xerrors.Errorf("serializing params: %w", err)
		}

		smsg, err := api.MpoolPushMessage(ctx, &types.Message{
			From:   mi.Worker,
			To:     maddr,
			Method: builtint.MethodsMiner.CompactSectorNumbers,
			Value:  big.Zero(),
			Params: sp,
		}, nil)
		if err != nil {
			return xerrors.Errorf("mpool push: %w", err)
		}

		fmt.Println("CompactSectorNumbers Message CID:", smsg.Cid())

		// wait for it to get mined into a block
		wait, err := api.StateWaitMsg(ctx, smsg.Cid(), build.MessageConfidence)
		if err != nil {
			return err
		}

		// check it executed successfully
		if wait.Receipt.ExitCode != 0 {
			fmt.Println("Propose owner change failed!")
			return err
		}

		return nil
	},
}

func isController(mi api.MinerInfo, addr address.Address) bool {
	if addr == mi.Owner || addr == mi.Worker {
		return true
	}

	for _, ca := range mi.ControlAddresses {
		if addr == ca {
			return true
		}
	}

	return false
}
