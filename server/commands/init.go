package commands

import (
	"math/big"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	ethUtils "github.com/ethereum/go-ethereum/cmd/utils"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/tendermint/tendermint/types"
	cmn "github.com/tendermint/tmlibs/common"

	emtUtils "github.com/CyberMiles/travis/modules/vm/cmd/utils"
)

var (
	FlagChainID = "chain-id"
)

var InitCmd = GetInitCmd()

func GetInitCmd() *cobra.Command {
	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize",
		RunE:  initFiles,
	}
	initCmd.Flags().String(FlagChainID, "local", "Chain ID")
	return initCmd
}

func initFiles(cmd *cobra.Command, args []string) error {
	initTendermint()
	return initEthermint()
}

func initTendermint() {
	// private validator
	privValFile := config.TMConfig.PrivValidatorFile()
	var privValidator *types.PrivValidatorFS
	if cmn.FileExists(privValFile) {
		privValidator = types.LoadPrivValidatorFS(privValFile)
		logger.Info("Found private validator", "path", privValFile)
	} else {
		privValidator = types.GenPrivValidatorFS(privValFile)
		privValidator.Save()
		logger.Info("Genetated private validator", "path", privValFile)
	}

	// genesis file
	genFile := config.TMConfig.GenesisFile()
	if cmn.FileExists(genFile) {
		logger.Info("Found genesis file", "path", genFile)
	} else {
		genDoc := types.GenesisDoc{
			ChainID: viper.GetString(FlagChainID),
		}
		genDoc.Validators = []types.GenesisValidator{{
			PubKey: privValidator.GetPubKey(),
			Power:  10,
		}}

		if err := genDoc.SaveAs(genFile); err != nil {
			panic(err)
		}
		logger.Info("Genetated genesis file", "path", genFile)
	}
}

func initEthermint() error {
	genesisPath := context.Args().First()
	genesis, err := emtUtils.ParseGenesisOrDefault(genesisPath)
	if err != nil {
		ethUtils.Fatalf("genesisJSON err: %v", err)
	}
	// override ethermint's chain_id
	genesis.Config.ChainId = new(big.Int).SetUint64(uint64(config.EMConfig.EthChainId))

	ethermintDataDir := emtUtils.MakeDataDir(context)

	chainDb, err := ethdb.NewLDBDatabase(filepath.Join(ethermintDataDir,
		"vm/chaindata"), 0, 0)
	if err != nil {
		ethUtils.Fatalf("could not open database: %v", err)
	}

	_, hash, err := core.SetupGenesisBlock(chainDb, genesis)
	if err != nil {
		ethUtils.Fatalf("failed to write genesis block: %v", err)
	}

	log.Info("successfully wrote genesis block and/or chain rule set", "hash", hash)

	// As per https://github.com/tendermint/ethermint/issues/244#issuecomment-322024199
	// Let's implicitly add in the respective keystore files
	// to avoid manually doing this step:
	// $ cp -r $GOPATH/src/github.com/tendermint/ethermint/setup/keystore $(DATADIR)
	keystoreDir := filepath.Join(ethermintDataDir, "keystore")
	if err := os.MkdirAll(keystoreDir, 0777); err != nil {
		ethUtils.Fatalf("mkdirAll keyStoreDir: %v", err)
	}

	for filename, content := range keystoreFilesMap {
		storeFileName := filepath.Join(keystoreDir, filename)
		f, err := os.Create(storeFileName)
		if err != nil {
			log.Error("create %q err: %v", storeFileName, err)
			continue
		}
		if _, err := f.Write([]byte(content)); err != nil {
			log.Error("write content %q err: %v", storeFileName, err)
		}
		if err := f.Close(); err != nil {
			return err
		}
	}

	return nil
}

var keystoreFilesMap = map[string]string{
	// https://github.com/tendermint/ethermint/blob/edc95f9d47ba1fb7c8161182533b5f5d5c5d619b/setup/keystore/UTC--2016-10-21T22-30-03.071787745Z--7eff122b94897ea5b0e2a9abf47b86337fafebdc
	// OR
	// $GOPATH/src/github.com/ethermint/setup/keystore/UTC--2016-10-21T22-30-03.071787745Z--7eff122b94897ea5b0e2a9abf47b86337fafebdc
	"UTC--2016-10-21T22-30-03.071787745Z--7eff122b94897ea5b0e2a9abf47b86337fafebdc": `
{
  "address":"7eff122b94897ea5b0e2a9abf47b86337fafebdc",
  "id":"f86a62b4-0621-4616-99af-c4b7f38fcc48","version":3,
  "crypto":{
    "cipher":"aes-128-ctr","ciphertext":"19de8a919e2f4cbdde2b7352ebd0be8ead2c87db35fc8e4c9acaf74aaaa57dad",
    "cipherparams":{"iv":"ba2bd370d6c9d5845e92fbc6f951c792"},
    "kdf":"scrypt","kdfparams":{"dklen":32,"n":262144,"p":1,"r":8,"salt":"c7cc2380a96adc9eb31d20bd8d8a7827199e8b16889582c0b9089da6a9f58e84"},
    "mac":"ff2c0caf051ca15d8c43b6f321ec10bd99bd654ddcf12dd1a28f730cc3c13730"
  }
}
`,
}