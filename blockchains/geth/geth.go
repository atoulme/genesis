package geth

import (
	db "../../db"
	state "../../state"
	util "../../util"
	helpers "../helpers"
	"encoding/json"
	"fmt"
	"github.com/Whiteblock/mustache"
	"log"
	"regexp"
	"strings"
	"sync"
)

var conf *util.Config

func init() {
	conf = util.GetConfig()
}

const ETH_NET_STATS_PORT = 3338

/**
 * Build the Ethereum Test Network
 * @param  map[string]interface{}   data    Configuration Data for the network
 * @param  int      nodes       The number of nodes in the network
 * @param  []Server servers     The list of servers passed from build
 */
func Build(details db.DeploymentDetails, servers []db.Server, clients []*util.SshClient,
	buildState *state.BuildState) ([]string, error) {

	mux := sync.Mutex{}
	ethconf, err := NewConf(details.Params)
	if err != nil {
		log.Println(err)
		return nil, err
	}

	buildState.SetBuildSteps(8 + (5 * details.Nodes))

	buildState.IncrementBuildProgress()

	/**Create the Password files**/
	{
		var data string
		for i := 1; i <= details.Nodes; i++ {
			data += "second\n"
		}
		err = buildState.Write("passwd", data)
		if err != nil {
			log.Println(err)
			return nil, err
		}
	}
	buildState.SetBuildStage("Distributing secrets")
	/**Copy over the password file**/

	err = helpers.CopyToServers(servers, clients, buildState, "passwd", "/home/appo/passwd")
	if err != nil {
		log.Println(err)
		return nil, err
	}
	buildState.IncrementBuildProgress()

	err = helpers.AllNodeExecCon(servers, buildState, func(serverNum int, localNodeNum int, absoluteNodeNum int) error {
		_, err := clients[serverNum].DockerExec(localNodeNum, "mkdir -p /geth")
		if err != nil {
			log.Println(err)
			return err
		}

		return clients[serverNum].DockerCp(localNodeNum, "/home/appo/passwd", "/geth/")
	})

	if err != nil {
		log.Println(err)
		return nil, err
	}

	/**Create the wallets**/
	wallets := make([]string, details.Nodes)
	rawWallets := make([]string, details.Nodes)
	buildState.SetBuildStage("Creating the wallets")

	err = helpers.AllNodeExecCon(servers, buildState, func(serverNum int, localNodeNum int, absoluteNodeNum int) error {
		gethResults, err := clients[serverNum].DockerExec(localNodeNum, "geth --datadir /geth/ --password /geth/passwd account new")
		if err != nil {
			log.Println(err)
			return err
		}

		addressPattern := regexp.MustCompile(`\{[A-z|0-9]+\}`)
		addresses := addressPattern.FindAllString(gethResults, -1)
		if len(addresses) < 1 {
			return fmt.Errorf("Unable to get addresses")
		}
		address := addresses[0]
		address = address[1 : len(address)-1]

		//fmt.Printf("Created wallet with address: %s\n",address)
		mux.Lock()
		wallets[absoluteNodeNum] = address
		mux.Unlock()

		buildState.IncrementBuildProgress()

		res, err := clients[serverNum].DockerExec(localNodeNum, "bash -c 'cat /geth/keystore/*'")
		if err != nil {
			log.Println(err)
			return err
		}
		mux.Lock()
		rawWallets[absoluteNodeNum] = strings.Replace(res, "\"", "\\\"", -1)
		mux.Unlock()

		return nil
	})
	if err != nil {
		log.Println(err)
		return nil, err
	}

	fmt.Printf("%v\n%v\n", wallets, rawWallets)
	buildState.IncrementBuildProgress()
	unlock := ""

	for i, wallet := range wallets {
		if i != 0 {
			unlock += ","
		}
		unlock += wallet
	}
	fmt.Printf("unlock = %s\n%+v\n\n", wallets, unlock)

	buildState.IncrementBuildProgress()
	buildState.SetBuildStage("Creating the genesis block")
	err = createGenesisfile(ethconf, details, wallets, buildState)
	if err != nil {
		log.Println(err)
		return nil, err
	}

	buildState.IncrementBuildProgress()
	buildState.SetBuildStage("Bootstrapping network")

	err = helpers.CopyToServers(servers, clients, buildState, "CustomGenesis.json", "/home/appo/CustomGenesis.json")
	if err != nil {
		log.Println(err)
		return nil, err
	}
	err = helpers.AllNodeExecCon(servers, buildState, func(serverNum int, localNodeNum int, absoluteNodeNum int) error {
		err := clients[serverNum].DockerCp(localNodeNum, "/home/appo/CustomGenesis.json", "/geth/")
		if err != nil {
			log.Println(err)
			return err
		}
		for i, rawWallet := range rawWallets {
			if i == absoluteNodeNum {
				continue
			}
			_, err = clients[serverNum].DockerExec(localNodeNum,
				fmt.Sprintf("bash -c 'echo \"%s\">>/geth/keystore/account%d'", rawWallet, i))
			if err != nil {
				log.Println(err)
				return err
			}
		}
		return nil
	})
	if err != nil {
		log.Println(err)
		return nil, err
	}

	staticNodes := make([]string, details.Nodes)

	buildState.SetBuildStage("Initializing geth")

	err = helpers.AllNodeExecCon(servers, buildState, func(serverNum int, localNodeNum int, absoluteNodeNum int) error {
		ip := servers[serverNum].Ips[localNodeNum]
		//Load the CustomGenesis file
		_, err := clients[serverNum].DockerExec(localNodeNum,
			fmt.Sprintf("geth --datadir /geth/ --networkid %d init /geth/CustomGenesis.json", ethconf.NetworkId))
		if err != nil {
			log.Println(err)
			return err
		}
		fmt.Printf("---------------------  CREATING block directory for NODE-%d ---------------------\n", absoluteNodeNum)
		gethResults, err := clients[serverNum].DockerExec(localNodeNum,
			fmt.Sprintf("bash -c 'echo -e \"admin.nodeInfo.enode\\nexit\\n\" | "+
				"geth --rpc --datadir /geth/ --networkid %d console'", ethconf.NetworkId))
		if err != nil {
			log.Println(err)
			return err
		}
		//fmt.Printf("RAWWWWWWWWWWWW%s\n\n\n",gethResults)
		enodePattern := regexp.MustCompile(`enode:\/\/[A-z|0-9]+@(\[\:\:\]|([0-9]|\.)+)\:[0-9]+`)
		enode := enodePattern.FindAllString(gethResults, 1)[0]
		//fmt.Printf("ENODE fetched is: %s\n",enode)
		enodeAddressPattern := regexp.MustCompile(`\[\:\:\]|([0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3})`)

		enode = enodeAddressPattern.ReplaceAllString(enode, ip)

		mux.Lock()
		staticNodes[absoluteNodeNum] = enode
		mux.Unlock()

		buildState.IncrementBuildProgress()
		return nil
	})
	if err != nil {
		log.Println(err)
		return nil, err
	}

	out, err := json.Marshal(staticNodes)
	if err != nil {
		log.Println(err)
		return nil, err
	}

	err = buildState.Write("static-nodes.json", string(out))
	if err != nil {
		log.Println(err)
		return nil, err
	}

	buildState.IncrementBuildProgress()
	buildState.SetBuildStage("Starting geth")
	//Copy static-nodes to every server
	err = helpers.CopyToServers(servers, clients, buildState, "static-nodes.json", "/home/appo/static-nodes.json")
	if err != nil {
		log.Println(err)
		return nil, err
	}

	err = helpers.AllNodeExecCon(servers, buildState, func(serverNum int, localNodeNum int, absoluteNodeNum int) error {
		ip := servers[serverNum].Ips[localNodeNum]
		buildState.IncrementBuildProgress()

		gethCmd := fmt.Sprintf(
			`geth --datadir /geth/ --maxpeers %d --networkid %d --rpc --nodiscover --rpcaddr %s`+
				` --rpcapi "web3,db,eth,net,personal,miner,txpool" --rpccorsdomain "0.0.0.0" --mine --unlock="%s"`+
				` --password /geth/passwd --etherbase %s console  2>&1 | tee %s`,
			ethconf.MaxPeers,
			ethconf.NetworkId,
			ip,
			unlock,
			wallets[absoluteNodeNum],
			conf.DockerOutputFile)

		err := clients[serverNum].DockerCp(localNodeNum, "/home/appo/static-nodes.json", "/geth/")
		if err != nil {
			log.Println(err)
			return err
		}

		_, err = clients[serverNum].DockerExecdit(localNodeNum, fmt.Sprintf("bash -ic '%s'", gethCmd))
		if err != nil {
			log.Println(err)
			return err
		}

		buildState.IncrementBuildProgress()
		return nil
	})
	if err != nil {
		log.Println(err)
		return nil, err
	}
	buildState.IncrementBuildProgress()

	err = setupEthNetStats(clients[0])
	if err != nil {
		log.Println(err)
		return nil, err
	}

	err = helpers.AllNodeExecCon(servers, buildState, func(serverNum int, localNodeNum int, absoluteNodeNum int) error {
		ip := servers[serverNum].Ips[localNodeNum]
		absName := fmt.Sprintf("%s%d", conf.NodePrefix, absoluteNodeNum)
		sedCmd := fmt.Sprintf(`sed -i -r 's/"INSTANCE_NAME"(\s)*:(\s)*"(\S)*"/"INSTANCE_NAME"\t: "%s"/g' /eth-net-intelligence-api/app.json`, absName)
		sedCmd2 := fmt.Sprintf(`sed -i -r 's/"WS_SERVER"(\s)*:(\s)*"(\S)*"/"WS_SERVER"\t: "http:\/\/%s:%d"/g' /eth-net-intelligence-api/app.json`,
			util.GetGateway(servers[serverNum].SubnetID, absoluteNodeNum), ETH_NET_STATS_PORT)
		sedCmd3 := fmt.Sprintf(`sed -i -r 's/"RPC_HOST"(\s)*:(\s)*"(\S)*"/"RPC_HOST"\t: "%s"/g' /eth-net-intelligence-api/app.json`, ip)

		//sedCmd3 := fmt.Sprintf("docker exec -it %s sed -i 's/\"WS_SECRET\"(\\s)*:(\\s)*\"[A-Z|a-z|0-9| ]*\"/\"WS_SECRET\"\\t: \"second\"/g' /eth-net-intelligence-api/app.json",container)

		_, err := clients[serverNum].DockerExec(localNodeNum, sedCmd)
		if err != nil {
			log.Println(err)
			return err
		}
		_, err = clients[serverNum].DockerExec(localNodeNum, sedCmd2)
		if err != nil {
			log.Println(err)
			return err
		}
		_, err = clients[serverNum].DockerExec(localNodeNum, sedCmd3)
		if err != nil {
			log.Println(err)
			return err
		}
		_, err = clients[serverNum].DockerExecd(localNodeNum, "bash -c 'cd /eth-net-intelligence-api && pm2 start app.json'")
		if err != nil {
			log.Println(err)
			return err
		}
		buildState.IncrementBuildProgress()
		return nil
	})
	return nil, err
}

/***************************************************************************************************************************/

func Add(details db.DeploymentDetails, servers []db.Server, clients []*util.SshClient,
	newNodes map[int][]string, buildState *state.BuildState) ([]string, error) {
	return nil, nil
}

func MakeFakeAccounts(accs int) []string {
	out := make([]string, accs)
	for i := 1; i <= accs; i++ {
		acc := fmt.Sprintf("%X", i)
		for j := len(acc); j < 40; j++ {
			acc = "0" + acc
		}
		acc = "0x" + acc
		out[i-1] = acc
	}
	return out
}

/**
 * Create the custom genesis file for Ethereum
 * @param  *EthConf ethconf     The chain configuration
 * @param  []string wallets     The wallets to be allocated a balance
 */

func createGenesisfile(ethconf *EthConf, details db.DeploymentDetails, wallets []string, buildState *state.BuildState) error {

	genesis := map[string]interface{}{
		"chainId":        ethconf.NetworkId,
		"homesteadBlock": ethconf.HomesteadBlock,
		"eip155Block":    ethconf.Eip155Block,
		"eip158Block":    ethconf.Eip158Block,
		"difficulty":     fmt.Sprintf("0x0%X", ethconf.Difficulty),
		"gasLimit":       fmt.Sprintf("0x0%X", ethconf.GasLimit),
	}
	alloc := map[string]map[string]string{}
	for _, wallet := range wallets {
		alloc[wallet] = map[string]string{
			"balance": ethconf.InitBalance,
		}
	}

	accs := MakeFakeAccounts(int(ethconf.ExtraAccounts))
	log.Println("Finished making fake accounts")

	for _, wallet := range accs {
		alloc[wallet] = map[string]string{
			"balance": ethconf.InitBalance,
		}
	}
	genesis["alloc"] = alloc
	dat, err := util.GetBlockchainConfig("geth", "genesis.json", details.Files)
	if err != nil {
		log.Println(err)
		return err
	}

	data, err := mustache.Render(string(dat), util.ConvertToStringMap(genesis))
	if err != nil {
		log.Println(err)
		return err
	}
	return buildState.Write("CustomGenesis.json", data)

}

/**
 * Setup Eth Net Stats on a server
 * @param  string    ip     The servers config
 */
func setupEthNetStats(client *util.SshClient) error {
	_, err := client.Run(fmt.Sprintf(
		"docker exec -d wb_service0 bash -c 'cd /eth-netstats && WS_SECRET=second PORT=%d npm start'", ETH_NET_STATS_PORT))
	if err != nil {
		log.Println(err)
		return err
	}
	return nil
}
