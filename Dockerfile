FROM golang:1.11.3-stretch

ADD . /genesis

#Currently depends on geth, however, this should be removed in the near future
RUN wget https://gethstore.blob.core.windows.net/builds/geth-linux-amd64-1.8.20-24d727b6.tar.gz &&\
tar -xzf geth-linux-amd64-1.8.20-24d727b6.tar.gz &&\
mv geth-linux-amd64-1.8.20-24d727b6/geth /bin/ &&\
rm -rf geth-linux-amd64-1.8.20-24d727b6*

RUN cd /genesis &&\
    go get || \
    go build

WORKDIR /genesis

ENTRYPOINT ["/genesis/genesis"]