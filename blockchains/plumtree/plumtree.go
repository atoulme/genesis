/*
	Copyright 2019 whiteblock Inc.
	This file is a part of the genesis.

	Genesis is free software: you can redistribute it and/or modify
    it under the terms of the GNU General Public License as published by
    the Free Software Foundation, either version 3 of the License, or
    (at your option) any later version.

    Genesis is distributed in the hope that it will be useful,
    but WITHOUT ANY WARRANTY; without even the implied warranty of
    MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
    GNU General Public License for more details.

    You should have received a copy of the GNU General Public License
    along with this program.  If not, see <https://www.gnu.org/licenses/>.
*/

//Package plumtree handles plumtree specific functionality
package plumtree

import (
	"fmt"

	"github.com/whiteblock/genesis/blockchains/helpers"
	"github.com/whiteblock/genesis/blockchains/registrar"
	"github.com/whiteblock/genesis/db"
	"github.com/whiteblock/genesis/ssh"
	"github.com/whiteblock/genesis/testnet"
	"github.com/whiteblock/genesis/util"
)

var conf *util.Config

const blockchain = "plumtree"

func init() {
	conf = util.GetConfig()
	registrar.RegisterBuild(blockchain, build)
	registrar.RegisterAddNodes(blockchain, add)
	registrar.RegisterServices(blockchain, getServices)
	registrar.RegisterDefaults(blockchain, getDefaults)
	registrar.RegisterParams(blockchain, getParams)
	registrar.RegisterAdditionalLogs(blockchain, map[string]string{
		"json": "/plumtree/data/log.json"})
}

func getParams() string {
	dat, err := helpers.GetStaticBlockchainConfig(blockchain, "params.json")
	if err != nil {
		panic(err) //Missing required files is a fatal error
	}
	return string(dat)
}

func getServices() []util.Service {
	return nil
}

func getDefaults() string {
	dat, err := helpers.GetStaticBlockchainConfig(blockchain, "defaults.json")
	if err != nil {
		panic(err) //Missing required files is a fatal error
	}
	return string(dat)
}

// build builds out a fresh new plumtree test network
func build(tn *testnet.TestNet) error {

	tn.BuildState.SetBuildSteps(0 + (tn.LDD.Nodes * 1))

	port := 9000
	peers := ""
	var peer string
	for i, node := range tn.Nodes {
		peer = fmt.Sprintf("tcp://whiteblock-node%d@%s:%d",
			node.LocalID,
			node.IP,
			port,
		)
		if i != len(tn.Nodes)-1 {
			peers = peers + " " + peer + " "
		} else {
			peers = peers + " " + peer
		}
		tn.BuildState.IncrementBuildProgress()
	}

	fmt.Println(peers)

	tn.BuildState.SetBuildStage("Starting plumtree")
	err := helpers.AllNodeExecCon(tn, func(client *ssh.Client, server *db.Server, node ssh.Node) error {
		defer tn.BuildState.IncrementBuildProgress()

		artemisCmd := "gossip -n 0.0.0.0 -l 9000 -r 9001 -m /plumtree/data/log.json --peer=" + peers + " 2>&1 | tee /output.log"

		_, err := client.DockerExecd(node, "tmux new -s whiteblock -d")
		if err != nil {
			return util.LogError(err)
		}

		_, err = client.DockerExecd(node, fmt.Sprintf("tmux send-keys -t whiteblock '%s' C-m", artemisCmd))
		return err
	})
	if err != nil {
		return util.LogError(err)
	}

	return nil
}

// Add handles adding a node to the plumtree testnet
// TODO
func add(tn *testnet.TestNet) error {
	return nil
}
