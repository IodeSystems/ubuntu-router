import { createFileRoute } from '@tanstack/react-router';
import { useState, useEffect } from 'react';
import {
  Box,
  Card,
  CardContent,
  Typography,
  Table,
  TableBody,
  TableCell,
  TableRow,
  Button,
  TextField,
  Grid,
  Chip,
  FormControl,
  InputLabel,
  Select,
  MenuItem,
  CircularProgress,
  Alert,
  RadioGroup,
  Radio,
  FormControlLabel,
  Tooltip,
  List,
  ListItem,
  ListItemButton,
  ListItemText,
  ListItemIcon,
  LinearProgress,
  InputAdornment,
  IconButton,
} from '@mui/material';
import {
  Search as DetectIcon,
  Wifi as WifiIcon,
  WifiLock as WifiLockIcon,
  Refresh as RefreshIcon,
  Visibility,
  VisibilityOff,
  SignalWifi4Bar,
  SignalWifi3Bar,
  SignalWifi2Bar,
  SignalWifi1Bar,
  SignalWifi0Bar,
} from '@mui/icons-material';
import { useQuery, useMutation } from '../api/hooks';
import type { ScannedWiFiNetwork, ChangeResponse } from '../api/client';
import { PendingConfirmBanner } from '../components/PendingConfirmBanner';

function getSignalIcon(quality: number) {
  if (quality >= 80) return <SignalWifi4Bar color="success" />;
  if (quality >= 60) return <SignalWifi3Bar color="success" />;
  if (quality >= 40) return <SignalWifi2Bar color="warning" />;
  if (quality >= 20) return <SignalWifi1Bar color="warning" />;
  return <SignalWifi0Bar color="error" />;
}

function WANPage() {
  const { data: statusData, isLoading: statusLoading, refetch } = useQuery('getWANStatus');
  const { data: interfacesData } = useQuery('listInterfaces');
  const { data: wifiCardsData } = useQuery('getWiFiCards');
  const { mutate: configureWAN, isLoading: isConfiguring } = useMutation('configureWAN');
  const { mutate: detectWAN, isLoading: isDetecting } = useMutation('detectWAN');
  const { mutate: scanWiFi, isLoading: isScanning } = useMutation('scanWiFiNetworks');
  const { mutate: connectWiFi, isLoading: isConnecting } = useMutation('connectWiFiWAN');
  const { mutate: disconnectWiFi, isLoading: isDisconnecting } = useMutation('disconnectWiFiWAN');

  const [config, setConfig] = useState({
    interface: '',
    mode: 'dhcp' as 'dhcp' | 'static' | 'pppoe' | 'wifi',
    staticIP: '',
    staticGateway: '',
    staticDNS: '',
    wifiSSID: '',
    wifiPassword: '',
    wifiSecurity: 'wpa2' as string,
  });
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);
  const [wifiNetworks, setWifiNetworks] = useState<ScannedWiFiNetwork[]>([]);
  const [showPassword, setShowPassword] = useState(false);

  // Initialize form from current config when data loads
  useEffect(() => {
    if (statusData?.config) {
      setConfig({
        interface: statusData.config.interface || '',
        mode: (statusData.config.mode as 'dhcp' | 'static' | 'pppoe' | 'wifi') || 'dhcp',
        staticIP: statusData.config.staticIP || '',
        staticGateway: statusData.config.staticGateway || '',
        staticDNS: statusData.config.staticDNS || '',
        wifiSSID: statusData.config.wifiSSID || '',
        wifiPassword: '',
        wifiSecurity: statusData.config.wifiSecurity || 'wpa2',
      });
    }
  }, [statusData]);

  // Filter to only ethernet interfaces (potential WAN interfaces)
  const ethernetInterfaces = interfacesData?.interfaces?.filter(
    (iface) => iface.type === 'ethernet' && iface.name !== 'lo'
  ) || [];

  // Get WiFi interfaces
  const wifiInterfaces = wifiCardsData?.cards?.map(card => card.interface) || [];

  const handleDetect = async () => {
    try {
      const result = await detectWAN(undefined);
      setConfig({
        ...config,
        interface: result.interface,
        mode: (result.mode as 'dhcp' | 'static' | 'pppoe' | 'wifi') || 'dhcp',
      });

      let msg = `Detected: ${result.interface}`;
      if (result.hasAddress && result.currentIP) {
        msg += ` (${result.currentIP})`;
      }
      if (result.currentGateway) {
        msg += ` via ${result.currentGateway}`;
      }
      setSuccess(msg);
      setError(null);
    } catch (e) {
      setError(String(e));
    }
  };

  const handleScanWiFi = async () => {
    try {
      const iface = config.interface || wifiInterfaces[0];
      if (!iface) {
        setError('No WiFi interface available');
        return;
      }
      const result = await scanWiFi({ interface: iface });
      setWifiNetworks(result.networks || []);
      setConfig(prev => ({ ...prev, interface: result.interface }));
      setError(null);
    } catch (e) {
      setError(String(e));
    }
  };

  const handleSelectNetwork = (network: ScannedWiFiNetwork) => {
    setConfig(prev => ({
      ...prev,
      wifiSSID: network.ssid,
      wifiSecurity: network.security,
      wifiPassword: network.security === 'open' ? '' : prev.wifiPassword,
    }));
  };

  const handleConnectWiFi = async () => {
    if (!config.wifiSSID) {
      setError('Please select a WiFi network');
      return;
    }
    if (config.wifiSecurity !== 'open' && !config.wifiPassword) {
      setError('Password is required for this network');
      return;
    }

    try {
      await connectWiFi({
        interface: config.interface || wifiInterfaces[0],
        ssid: config.wifiSSID,
        password: config.wifiPassword,
        security: config.wifiSecurity as 'open' | 'wep' | 'wpa' | 'wpa2' | 'wpa3',
      });
      setSuccess(`Connected to ${config.wifiSSID}`);
      setError(null);
      refetch();
    } catch (e) {
      setError(String(e));
    }
  };

  const handleDisconnectWiFi = async () => {
    try {
      await disconnectWiFi({ interface: config.interface });
      setSuccess('Disconnected from WiFi');
      setError(null);
      refetch();
    } catch (e) {
      setError(String(e));
    }
  };

  const handleConfigure = async () => {
    if (!config.interface) {
      setError('Interface is required');
      return;
    }
    if (config.mode === 'static') {
      if (!config.staticIP) {
        setError('IP Address is required for static mode');
        return;
      }
      if (!config.staticGateway) {
        setError('Gateway is required for static mode');
        return;
      }
    }

    try {
      const result = await configureWAN({
        interface: config.interface,
        mode: config.mode,
        static_ip: config.staticIP,
        static_gateway: config.staticGateway,
        static_dns: config.staticDNS,
        wifi_ssid: config.wifiSSID,
        wifi_password: config.wifiPassword,
        wifi_security: config.wifiSecurity,
      }) as ChangeResponse;

      if (result.pending_confirmation) {
        setSuccess('Configuration applied. Please confirm within 30 seconds or changes will be reverted.');
      } else if (result.success) {
        setSuccess('WAN configuration applied');
      } else {
        setError(result.error || 'Configuration failed');
        return;
      }
      setError(null);
      refetch();
    } catch (e) {
      setError(String(e));
    }
  };

  const isWiFiMode = config.mode === 'wifi';
  const hasWiFiInterfaces = wifiInterfaces.length > 0;

  return (
    <Box>
      <Typography variant="h4" gutterBottom>
        WAN Configuration
      </Typography>

      {error && <Alert severity="error" sx={{ mb: 2 }}>{error}</Alert>}
      {success && <Alert severity="success" sx={{ mb: 2 }}>{success}</Alert>}

      {/* Pending Configuration Banner */}
      <PendingConfirmBanner
        onConfirmed={() => {
          setSuccess('Configuration confirmed and saved.');
          refetch();
        }}
        onCancelled={() => {
          setSuccess('Configuration rolled back to previous state.');
          refetch();
        }}
      />

      {/* Current Status */}
      <Card sx={{ mb: 3 }}>
        <CardContent>
          <Typography variant="h6" color="primary" gutterBottom>
            Current Status
          </Typography>
          {statusLoading ? (
            <CircularProgress size={24} />
          ) : statusData?.interface ? (
            <Table size="small">
              <TableBody>
                <TableRow>
                  <TableCell sx={{ width: 150 }}>Interface</TableCell>
                  <TableCell>
                    <code>{statusData.interface.name}</code>
                    {' '}
                    {statusData.interface.state === 'up' ? (
                      <Chip label="Up" color="success" size="small" />
                    ) : (
                      <Chip label="Down" color="error" size="small" />
                    )}
                  </TableCell>
                </TableRow>
                <TableRow>
                  <TableCell>Mode</TableCell>
                  <TableCell>
                    <Chip
                      label={statusData.config.mode?.toUpperCase() || 'Not configured'}
                      size="small"
                      variant="outlined"
                      icon={statusData.config.mode === 'wifi' ? <WifiIcon /> : undefined}
                    />
                  </TableCell>
                </TableRow>
                {statusData.config.mode === 'wifi' && statusData.wifiStatus && (
                  <>
                    <TableRow>
                      <TableCell>WiFi Network</TableCell>
                      <TableCell>
                        {statusData.wifiStatus.connected ? (
                          <>
                            <strong>{statusData.wifiStatus.ssid}</strong>
                            {' '}
                            <Chip label="Connected" color="success" size="small" />
                            {statusData.wifiStatus.signal && (
                              <Typography component="span" sx={{ ml: 1 }}>
                                ({statusData.wifiStatus.signal} dBm)
                              </Typography>
                            )}
                          </>
                        ) : (
                          <Chip label="Disconnected" color="error" size="small" />
                        )}
                      </TableCell>
                    </TableRow>
                  </>
                )}
                <TableRow>
                  <TableCell>IP Address</TableCell>
                  <TableCell>
                    {statusData.interface.addresses?.length > 0
                      ? statusData.interface.addresses.join(', ')
                      : statusData.wifiStatus?.ip_address || (
                        <Typography color="text.secondary">None</Typography>
                      )
                    }
                  </TableCell>
                </TableRow>
                <TableRow>
                  <TableCell>Gateway</TableCell>
                  <TableCell>
                    {statusData.interface.gateway || statusData.wifiStatus?.gateway || (
                      <Typography color="text.secondary">None</Typography>
                    )}
                  </TableCell>
                </TableRow>
              </TableBody>
            </Table>
          ) : (
            <Typography color="text.secondary">
              WAN interface not configured
            </Typography>
          )}
          {statusData?.config.mode === 'wifi' && statusData?.wifiStatus?.connected && (
            <Button
              variant="outlined"
              color="warning"
              onClick={handleDisconnectWiFi}
              disabled={isDisconnecting}
              sx={{ mt: 2 }}
            >
              {isDisconnecting ? 'Disconnecting...' : 'Disconnect WiFi'}
            </Button>
          )}
        </CardContent>
      </Card>

      {/* Configuration Form */}
      <Card>
        <CardContent>
          <Typography variant="h6" color="primary" gutterBottom>
            Configure WAN
          </Typography>
          <Grid container spacing={2}>
            {/* Mode Selection */}
            <Grid size={12}>
              <Typography variant="subtitle2" gutterBottom>
                Connection Mode
              </Typography>
              <FormControl>
                <RadioGroup
                  row
                  value={config.mode}
                  onChange={(e) => {
                    const newMode = e.target.value as 'dhcp' | 'static' | 'pppoe' | 'wifi';
                    setConfig({ ...config, mode: newMode });
                    // Auto-select WiFi interface when switching to wifi mode
                    if (newMode === 'wifi' && wifiInterfaces.length > 0) {
                      setConfig(prev => ({ ...prev, mode: newMode, interface: wifiInterfaces[0] }));
                    }
                  }}
                >
                  <FormControlLabel value="dhcp" control={<Radio />} label="DHCP (Automatic)" />
                  <FormControlLabel value="static" control={<Radio />} label="Static IP" />
                  <Tooltip title={hasWiFiInterfaces ? "Connect via WiFi" : "No WiFi interfaces detected"}>
                    <FormControlLabel
                      value="wifi"
                      control={<Radio />}
                      label={
                        <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
                          <WifiIcon fontSize="small" />
                          WiFi
                        </Box>
                      }
                      disabled={!hasWiFiInterfaces}
                    />
                  </Tooltip>
                  <Tooltip title="PPPoE is not yet implemented">
                    <FormControlLabel
                      value="pppoe"
                      control={<Radio />}
                      label="PPPoE"
                      disabled
                    />
                  </Tooltip>
                </RadioGroup>
              </FormControl>
            </Grid>

            {/* Interface Selection (for non-WiFi modes) */}
            {!isWiFiMode && (
              <>
                <Grid size={{ xs: 12, md: 8 }}>
                  <FormControl fullWidth size="small">
                    <InputLabel>Interface</InputLabel>
                    <Select
                      value={config.interface}
                      label="Interface"
                      onChange={(e) => setConfig({ ...config, interface: e.target.value })}
                    >
                      {ethernetInterfaces.map((iface) => (
                        <MenuItem key={iface.name} value={iface.name}>
                          {iface.name}
                          {iface.addresses?.length > 0 && (
                            <Typography component="span" color="text.secondary" sx={{ ml: 1 }}>
                              ({iface.addresses.map(a => a.ip).join(', ')})
                            </Typography>
                          )}
                        </MenuItem>
                      ))}
                    </Select>
                  </FormControl>
                </Grid>
                <Grid size={{ xs: 12, md: 4 }}>
                  <Button
                    variant="outlined"
                    startIcon={<DetectIcon />}
                    onClick={handleDetect}
                    disabled={isDetecting}
                    fullWidth
                    sx={{ height: '40px' }}
                  >
                    Auto-detect
                  </Button>
                </Grid>
              </>
            )}

            {/* WiFi Mode UI */}
            {isWiFiMode && (
              <>
                {/* WiFi Interface Selection */}
                <Grid size={{ xs: 12, md: 6 }}>
                  <FormControl fullWidth size="small">
                    <InputLabel>WiFi Interface</InputLabel>
                    <Select
                      value={config.interface || wifiInterfaces[0] || ''}
                      label="WiFi Interface"
                      onChange={(e) => setConfig({ ...config, interface: e.target.value })}
                    >
                      {wifiInterfaces.map((iface) => (
                        <MenuItem key={iface} value={iface}>
                          {iface}
                        </MenuItem>
                      ))}
                    </Select>
                  </FormControl>
                </Grid>
                <Grid size={{ xs: 12, md: 6 }}>
                  <Button
                    variant="outlined"
                    startIcon={isScanning ? <CircularProgress size={20} /> : <RefreshIcon />}
                    onClick={handleScanWiFi}
                    disabled={isScanning}
                    fullWidth
                    sx={{ height: '40px' }}
                  >
                    {isScanning ? 'Scanning...' : 'Scan Networks'}
                  </Button>
                </Grid>

                {/* Network List */}
                {wifiNetworks.length > 0 && (
                  <Grid size={12}>
                    <Typography variant="subtitle2" gutterBottom>
                      Available Networks
                    </Typography>
                    <List dense sx={{ maxHeight: 300, overflow: 'auto', border: 1, borderColor: 'divider', borderRadius: 1 }}>
                      {wifiNetworks.map((network, idx) => (
                        <ListItem key={`${network.bssid}-${idx}`} disablePadding>
                          <ListItemButton
                            selected={config.wifiSSID === network.ssid}
                            onClick={() => handleSelectNetwork(network)}
                          >
                            <ListItemIcon>
                              {network.security === 'open' ? (
                                <WifiIcon color={network.connected ? 'success' : 'inherit'} />
                              ) : (
                                <WifiLockIcon color={network.connected ? 'success' : 'inherit'} />
                              )}
                            </ListItemIcon>
                            <ListItemText
                              primary={
                                <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                                  {network.ssid || '(Hidden)'}
                                  {network.connected && (
                                    <Chip label="Connected" size="small" color="success" />
                                  )}
                                </Box>
                              }
                              secondary={`${network.security.toUpperCase()} - ${network.band} - Ch ${network.channel}`}
                            />
                            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                              {getSignalIcon(network.quality)}
                              <Typography variant="body2" color="text.secondary">
                                {network.quality}%
                              </Typography>
                            </Box>
                          </ListItemButton>
                        </ListItem>
                      ))}
                    </List>
                  </Grid>
                )}

                {/* Manual SSID Entry */}
                <Grid size={{ xs: 12, md: 6 }}>
                  <TextField
                    fullWidth
                    label="Network Name (SSID)"
                    size="small"
                    value={config.wifiSSID}
                    onChange={(e) => setConfig({ ...config, wifiSSID: e.target.value })}
                    placeholder="Enter SSID or select from scan"
                  />
                </Grid>
                <Grid size={{ xs: 12, md: 6 }}>
                  <FormControl fullWidth size="small">
                    <InputLabel>Security</InputLabel>
                    <Select
                      value={config.wifiSecurity}
                      label="Security"
                      onChange={(e) => setConfig({ ...config, wifiSecurity: e.target.value })}
                    >
                      <MenuItem value="open">Open (No Password)</MenuItem>
                      <MenuItem value="wep">WEP</MenuItem>
                      <MenuItem value="wpa">WPA</MenuItem>
                      <MenuItem value="wpa2">WPA2</MenuItem>
                      <MenuItem value="wpa3">WPA3</MenuItem>
                    </Select>
                  </FormControl>
                </Grid>

                {/* Password Field */}
                {config.wifiSecurity !== 'open' && (
                  <Grid size={12}>
                    <TextField
                      fullWidth
                      label="Password"
                      size="small"
                      type={showPassword ? 'text' : 'password'}
                      value={config.wifiPassword}
                      onChange={(e) => setConfig({ ...config, wifiPassword: e.target.value })}
                      InputProps={{
                        endAdornment: (
                          <InputAdornment position="end">
                            <IconButton
                              onClick={() => setShowPassword(!showPassword)}
                              edge="end"
                            >
                              {showPassword ? <VisibilityOff /> : <Visibility />}
                            </IconButton>
                          </InputAdornment>
                        ),
                      }}
                    />
                  </Grid>
                )}

                {/* Connect Button */}
                <Grid size={12}>
                  {isConnecting && <LinearProgress sx={{ mb: 1 }} />}
                  <Button
                    variant="contained"
                    onClick={handleConnectWiFi}
                    disabled={isConnecting || !config.wifiSSID}
                    startIcon={<WifiIcon />}
                  >
                    {isConnecting ? 'Connecting...' : 'Connect to WiFi'}
                  </Button>
                </Grid>
              </>
            )}

            {/* Static IP Fields (shown only when mode is static) */}
            {config.mode === 'static' && (
              <>
                <Grid size={{ xs: 12, md: 6 }}>
                  <TextField
                    fullWidth
                    label="IP Address"
                    size="small"
                    value={config.staticIP}
                    onChange={(e) => setConfig({ ...config, staticIP: e.target.value })}
                    placeholder="192.168.1.2/24"
                    helperText="Include CIDR notation (e.g., /24)"
                  />
                </Grid>
                <Grid size={{ xs: 12, md: 6 }}>
                  <TextField
                    fullWidth
                    label="Gateway"
                    size="small"
                    value={config.staticGateway}
                    onChange={(e) => setConfig({ ...config, staticGateway: e.target.value })}
                    placeholder="192.168.1.1"
                  />
                </Grid>
                <Grid size={{ xs: 12, md: 6 }}>
                  <TextField
                    fullWidth
                    label="DNS Servers"
                    size="small"
                    value={config.staticDNS}
                    onChange={(e) => setConfig({ ...config, staticDNS: e.target.value })}
                    placeholder="8.8.8.8,8.8.4.4"
                    helperText="Comma-separated DNS servers"
                  />
                </Grid>
              </>
            )}

            {/* Apply Button (for non-WiFi modes) */}
            {!isWiFiMode && (
              <Grid size={12}>
                <Button
                  variant="contained"
                  onClick={handleConfigure}
                  disabled={isConfiguring || !config.interface}
                >
                  {isConfiguring ? 'Applying...' : 'Apply Configuration'}
                </Button>
              </Grid>
            )}
          </Grid>
        </CardContent>
      </Card>
    </Box>
  );
}

export const Route = createFileRoute('/wan')({
  component: WANPage,
});
