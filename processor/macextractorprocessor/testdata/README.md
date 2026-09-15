# Test Data

Real production log samples lifted from ELK.

These files are used by `TestExtractMACs_FromTestdataFiles` in `macextract_test.go`.

## Files

| File | Format | Expected MAC |
|------|--------|--------------|
| cisco_mac_flapping.txt | Cisco (aabb.ccdd.eeff) | 26:d9:e0:17:39:3d |
| dhcp_request_standard.txt | Standard (aa:bb:cc:dd:ee:ff) | 10:db:00:db:15:02 |
| dhcp_ack_with_hostname.txt | Standard | c4:4f:33:91:a6:58 |
| dhcpv6_solicit_eui64.txt | IPv6 EUI-64 (fe80::XXff:feXX) | 00:25:90:8b:db:a0 |
| dhcpv6_reply_with_duid.txt | DUID-LL (00:03:00:01:MAC) | 26:d9:e0:17:39:3d |

## Supported Formats

### Standard MAC Formats
- Colon-separated: `aa:bb:cc:dd:ee:ff`
- Dash-separated: `aa-bb-cc-dd-ee-ff`
- Cisco: `aabb.ccdd.eeff`

### DHCPv6 DUID Formats
- DUID-LL (type 3): `00:03:00:01:XX:XX:XX:XX:XX:XX` (type + hw-type + MAC)
- DUID-LLT (type 1): `00:01:00:01:TT:TT:TT:TT:XX:XX:XX:XX:XX:XX` (type + hw-type + timestamp + MAC)

### IPv6 EUI-64 Format
- Link-local addresses with EUI-64: `fe80::XXxx:XXff:feXX:XXXX`
- The `ff:fe` in the middle indicates EUI-64 encoding
- The 7th bit of the first byte is flipped (Universal/Local bit)
