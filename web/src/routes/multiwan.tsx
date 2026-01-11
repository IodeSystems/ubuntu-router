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
  TableHead,
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
  Switch,
  FormControlLabel,
  IconButton,
  Tooltip,
  LinearProgress,
} from '@mui/material';
import {
  Add as AddIcon,
  Delete as DeleteIcon,
  SwapVert as SwapIcon,
  Refresh as RefreshIcon,
  CheckCircle,
  Warning,
  Error as ErrorIcon,
  ArrowUpward,
  ArrowDownward,
} from '@mui/icons-material';
import { useQuery, useMutation } from '../api/hooks';
import type { WANConfigItem, WANStatusItem, MultiWANConfig } from '../api/client';

function MultiWANPage() {
  const { data: statusData, isLoading: statusLoading, refetch } = useQuery('getMultiWANStatus');
  const { data: interfacesData } = useQuery('listInterfaces');
  const { data: wifiCardsData } = useQuery('getWiFiCards');
  const { mutate: configureMultiWAN, isLoading: isConfiguring } = useMutation('configureMultiWAN');
  const { mutate: switchWAN, isLoading: isSwitching } = useMutation('switchMultiWAN');

  const [config, setConfig] = useState<MultiWANConfig>({
    enabled: false,
    mode: 'failover',
    wans: [],
    failback_delay: 60,
  });
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);

  // Get available interfaces
  const availableInterfaces = [
    ...(interfacesData?.interfaces?.filter(
      (iface) => iface.type === 'ethernet' && iface.name !== 'lo'
    ).map(i => i.name) || []),
    ...(wifiCardsData?.cards?.map(card => card.interface) || []),
  ];

  // Initialize config from status when available
  useEffect(() => {
    if (statusData && statusData.enabled) {
      // Try to reconstruct config from status
      const wans: WANConfigItem[] = statusData.wans?.map(ws => ({
        name: ws.name,
        interface: ws.interface,
        enabled: ws.enabled,
        priority: ws.priority,
        mode: 'dhcp' as const,
        health_check_enabled: true,
      })) || [];

      setConfig({
        enabled: statusData.enabled,
        mode: statusData.mode as 'failover' | 'loadbalance',
        wans,
        failback_delay: 60,
      });
    }
  }, [statusData]);

  const handleAddWAN = () => {
    const usedInterfaces = config.wans.map(w => w.interface);
    const availableIface = availableInterfaces.find(i => !usedInterfaces.includes(i));

    if (!availableIface) {
      setError('No more interfaces available');
      return;
    }

    const newWAN: WANConfigItem = {
      name: config.wans.length === 0 ? 'Primary' : `Backup ${config.wans.length}`,
      interface: availableIface,
      enabled: true,
      priority: config.wans.length,
      mode: 'dhcp',
      health_check_enabled: true,
      health_check_interval: 10,
      health_check_timeout: 5,
      health_check_retries: 3,
    };

    setConfig(prev => ({
      ...prev,
      wans: [...prev.wans, newWAN],
    }));
  };

  const handleRemoveWAN = (index: number) => {
    setConfig(prev => ({
      ...prev,
      wans: prev.wans.filter((_, i) => i !== index),
    }));
  };

  const handleUpdateWAN = (index: number, updates: Partial<WANConfigItem>) => {
    setConfig(prev => ({
      ...prev,
      wans: prev.wans.map((wan, i) => i === index ? { ...wan, ...updates } : wan),
    }));
  };

  const handleMovePriority = (index: number, direction: 'up' | 'down') => {
    const newIndex = direction === 'up' ? index - 1 : index + 1;
    if (newIndex < 0 || newIndex >= config.wans.length) return;

    const newWans = [...config.wans];
    [newWans[index], newWans[newIndex]] = [newWans[newIndex], newWans[index]];

    // Update priorities
    newWans.forEach((wan, i) => {
      wan.priority = i;
    });

    setConfig(prev => ({ ...prev, wans: newWans }));
  };

  const handleSave = async () => {
    try {
      await configureMultiWAN(config);
      setSuccess('Multi-WAN configuration saved');
      setError(null);
      refetch();
    } catch (e) {
      setError(String(e));
    }
  };

  const handleForceSwitch = async (iface: string) => {
    try {
      await switchWAN({ interface: iface });
      setSuccess(`Switched to ${iface}`);
      setError(null);
      refetch();
    } catch (e) {
      setError(String(e));
    }
  };

  const getStatusIcon = (wan: WANStatusItem) => {
    if (!wan.enabled) return <Chip label="Disabled" size="small" />;
    if (!wan.up) return <ErrorIcon color="error" />;
    if (!wan.healthy) return <Warning color="warning" />;
    return <CheckCircle color="success" />;
  };

  return (
    <Box>
      <Typography variant="h4" gutterBottom>
        Multi-WAN Failover
      </Typography>

      {error && <Alert severity="error" sx={{ mb: 2 }}>{error}</Alert>}
      {success && <Alert severity="success" sx={{ mb: 2 }}>{success}</Alert>}

      {/* Current Status */}
      <Card sx={{ mb: 3 }}>
        <CardContent>
          <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: 2 }}>
            <Typography variant="h6" color="primary">
              Status
            </Typography>
            <Button
              startIcon={<RefreshIcon />}
              onClick={() => refetch()}
              size="small"
            >
              Refresh
            </Button>
          </Box>

          {statusLoading ? (
            <CircularProgress size={24} />
          ) : !statusData?.enabled ? (
            <Typography color="text.secondary">
              Multi-WAN failover is not enabled
            </Typography>
          ) : (
            <>
              <Box sx={{ mb: 2 }}>
                <Chip
                  label={`Mode: ${statusData.mode?.toUpperCase()}`}
                  size="small"
                  sx={{ mr: 1 }}
                />
                <Chip
                  label={`Active: ${statusData.active_wan || 'None'}`}
                  color="primary"
                  size="small"
                  sx={{ mr: 1 }}
                />
                <Chip
                  label={`Switches: ${statusData.switch_count}`}
                  variant="outlined"
                  size="small"
                />
              </Box>

              <Table size="small">
                <TableHead>
                  <TableRow>
                    <TableCell>Priority</TableCell>
                    <TableCell>Name</TableCell>
                    <TableCell>Interface</TableCell>
                    <TableCell>Status</TableCell>
                    <TableCell>IP</TableCell>
                    <TableCell>Gateway</TableCell>
                    <TableCell>Health</TableCell>
                    <TableCell>Actions</TableCell>
                  </TableRow>
                </TableHead>
                <TableBody>
                  {statusData.wans?.map((wan: WANStatusItem) => (
                    <TableRow
                      key={wan.interface}
                      sx={{ bgcolor: wan.active ? 'action.selected' : 'inherit' }}
                    >
                      <TableCell>{wan.priority}</TableCell>
                      <TableCell>
                        {wan.name}
                        {wan.active && <Chip label="Active" color="success" size="small" sx={{ ml: 1 }} />}
                      </TableCell>
                      <TableCell><code>{wan.interface}</code></TableCell>
                      <TableCell>{getStatusIcon(wan)}</TableCell>
                      <TableCell>{wan.ip_address || '-'}</TableCell>
                      <TableCell>{wan.gateway || '-'}</TableCell>
                      <TableCell>
                        <Tooltip title={`${wan.checks_passed}/${wan.checks_run} checks passed, ${wan.failure_count} consecutive failures`}>
                          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                            <LinearProgress
                              variant="determinate"
                              value={wan.checks_run > 0 ? (wan.checks_passed / wan.checks_run) * 100 : 0}
                              sx={{ width: 50, height: 6, borderRadius: 1 }}
                              color={wan.healthy ? 'success' : 'error'}
                            />
                            <Typography variant="caption">
                              {wan.checks_run > 0 ? Math.round((wan.checks_passed / wan.checks_run) * 100) : 0}%
                            </Typography>
                          </Box>
                        </Tooltip>
                      </TableCell>
                      <TableCell>
                        {!wan.active && wan.healthy && (
                          <Button
                            size="small"
                            startIcon={<SwapIcon />}
                            onClick={() => handleForceSwitch(wan.interface)}
                            disabled={isSwitching}
                          >
                            Switch
                          </Button>
                        )}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </>
          )}
        </CardContent>
      </Card>

      {/* Configuration */}
      <Card>
        <CardContent>
          <Typography variant="h6" color="primary" gutterBottom>
            Configuration
          </Typography>

          <Grid container spacing={2}>
            <Grid size={12}>
              <FormControlLabel
                control={
                  <Switch
                    checked={config.enabled}
                    onChange={(e) => setConfig(prev => ({ ...prev, enabled: e.target.checked }))}
                  />
                }
                label="Enable Multi-WAN Failover"
              />
            </Grid>

            {config.enabled && (
              <>
                <Grid size={{ xs: 12, md: 6 }}>
                  <FormControl fullWidth size="small">
                    <InputLabel>Mode</InputLabel>
                    <Select
                      value={config.mode}
                      label="Mode"
                      onChange={(e) => setConfig(prev => ({ ...prev, mode: e.target.value as 'failover' | 'loadbalance' }))}
                    >
                      <MenuItem value="failover">Failover (Primary/Backup)</MenuItem>
                      <MenuItem value="loadbalance" disabled>Load Balance (Coming Soon)</MenuItem>
                    </Select>
                  </FormControl>
                </Grid>
                <Grid size={{ xs: 12, md: 6 }}>
                  <TextField
                    fullWidth
                    label="Failback Delay (seconds)"
                    type="number"
                    size="small"
                    value={config.failback_delay}
                    onChange={(e) => setConfig(prev => ({ ...prev, failback_delay: parseInt(e.target.value) || 60 }))}
                    helperText="Wait before switching back to primary"
                  />
                </Grid>

                {/* WAN List */}
                <Grid size={12}>
                  <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: 1 }}>
                    <Typography variant="subtitle2">
                      WAN Interfaces (ordered by priority)
                    </Typography>
                    <Button
                      startIcon={<AddIcon />}
                      onClick={handleAddWAN}
                      size="small"
                      disabled={config.wans.length >= availableInterfaces.length}
                    >
                      Add WAN
                    </Button>
                  </Box>

                  {config.wans.length === 0 ? (
                    <Alert severity="info">
                      Add at least two WAN interfaces to enable failover
                    </Alert>
                  ) : (
                    <Table size="small">
                      <TableHead>
                        <TableRow>
                          <TableCell width={80}>Priority</TableCell>
                          <TableCell>Name</TableCell>
                          <TableCell>Interface</TableCell>
                          <TableCell>Mode</TableCell>
                          <TableCell>Health Check</TableCell>
                          <TableCell width={100}>Actions</TableCell>
                        </TableRow>
                      </TableHead>
                      <TableBody>
                        {config.wans.map((wan, index) => (
                          <TableRow key={index}>
                            <TableCell>
                              <Box sx={{ display: 'flex', alignItems: 'center' }}>
                                <IconButton
                                  size="small"
                                  onClick={() => handleMovePriority(index, 'up')}
                                  disabled={index === 0}
                                >
                                  <ArrowUpward fontSize="small" />
                                </IconButton>
                                <IconButton
                                  size="small"
                                  onClick={() => handleMovePriority(index, 'down')}
                                  disabled={index === config.wans.length - 1}
                                >
                                  <ArrowDownward fontSize="small" />
                                </IconButton>
                              </Box>
                            </TableCell>
                            <TableCell>
                              <TextField
                                size="small"
                                value={wan.name}
                                onChange={(e) => handleUpdateWAN(index, { name: e.target.value })}
                                variant="standard"
                              />
                            </TableCell>
                            <TableCell>
                              <FormControl size="small" sx={{ minWidth: 120 }}>
                                <Select
                                  value={wan.interface}
                                  onChange={(e) => handleUpdateWAN(index, { interface: e.target.value })}
                                  variant="standard"
                                >
                                  {availableInterfaces.map((iface) => (
                                    <MenuItem
                                      key={iface}
                                      value={iface}
                                      disabled={config.wans.some((w, i) => i !== index && w.interface === iface)}
                                    >
                                      {iface}
                                    </MenuItem>
                                  ))}
                                </Select>
                              </FormControl>
                            </TableCell>
                            <TableCell>
                              <FormControl size="small" sx={{ minWidth: 100 }}>
                                <Select
                                  value={wan.mode}
                                  onChange={(e) => handleUpdateWAN(index, { mode: e.target.value as 'dhcp' | 'static' | 'wifi' })}
                                  variant="standard"
                                >
                                  <MenuItem value="dhcp">DHCP</MenuItem>
                                  <MenuItem value="static">Static</MenuItem>
                                  <MenuItem value="wifi">WiFi</MenuItem>
                                </Select>
                              </FormControl>
                            </TableCell>
                            <TableCell>
                              <Switch
                                size="small"
                                checked={wan.health_check_enabled}
                                onChange={(e) => handleUpdateWAN(index, { health_check_enabled: e.target.checked })}
                              />
                            </TableCell>
                            <TableCell>
                              <IconButton
                                size="small"
                                color="error"
                                onClick={() => handleRemoveWAN(index)}
                              >
                                <DeleteIcon />
                              </IconButton>
                            </TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  )}
                </Grid>
              </>
            )}

            <Grid size={12}>
              <Button
                variant="contained"
                onClick={handleSave}
                disabled={isConfiguring}
              >
                {isConfiguring ? 'Saving...' : 'Save Configuration'}
              </Button>
            </Grid>
          </Grid>
        </CardContent>
      </Card>
    </Box>
  );
}

export const Route = createFileRoute('/multiwan')({
  component: MultiWANPage,
});
