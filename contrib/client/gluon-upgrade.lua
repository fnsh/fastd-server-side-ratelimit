#!/usr/bin/lua

local uci = require('simple-uci').cursor()
local platform = require 'gluon.platform'

local limit_enabled = uci:get_bool('gluon', 'mesh_vpn', 'limit_enabled')
local rate_downstream = uci:get('gluon', 'mesh_vpn', 'limit_ingress')
local rate_upstream = uci:get('gluon', 'mesh_vpn', 'limit_egress')

if not limit_enabled then
	return
end

-- Need to remove simple-tc and sqm rules
uci:delete('simple-tc', 'mesh_vpn')
uci:delete('sqm', 'mesh_vpn')

-- Add IPv6 LL to mesh-vpn interface

uci:section('network', 'interface', 'mesh_vpn_fssrl', {
	ifname = 'mesh-vpn',
	proto = 'static',
})
uci:set_list('network', 'mesh_vpn_fssrl', 'ip6addr', {'fe80::f421:d:2/64'})

-- Configure own rules
uci:section('fssrl', 'fssrl', 'vpn', {
	enabled = '1',
	interface = 'mesh-vpn',
	minimum_downstream = rate_downstream,
	minimum_upstream = rate_upstream,
	maximum_downstream = rate_downstream,
	maximum_upstream = rate_upstream,
	script = '/usr/share/fssrl/apply-rate-limit.sh',
	target = platform.get_target(),
	subtarget = platform.get_subtarget(),
})

uci:save('network')
uci:save('simple-tc')
uci:save('sqm')
uci:save('fssrl')
