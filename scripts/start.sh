#!/bin/sh

mkdir -p /var/log/amnezia
chmod 755 /var/log/amnezia

lsmod | grep -E "^nf_tables|^nft_"
nft_true=$?

if [ "$nft_true" -ne 0 ]; then
    ln -sf /sbin/iptables-legacy /sbin/iptables
    echo "iptables-legacy set as default"
fi

exec /usr/bin/api
