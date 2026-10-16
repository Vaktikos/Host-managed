import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError, api } from '../api/client'
import type { VMDetail } from '../api/types'
import { useAuth } from '../auth'
import { ErrorAlert, useToast } from '../components/ui'
import { CloudInitForm, cloudInitErrors, draftFrom, toCloudInit } from './CloudInitForm'

export function VMCloudInit({ vm, onChanged }: { vm: VMDetail; onChanged: () => void }) {
  const { t } = useTranslation()
  const { can } = useAuth()
  const toast = useToast()
  const [d, setD] = useState(draftFrom(vm.cloud_init, vm.name))
  const [err, setErr] = useState<ApiError>()
  const errors = cloudInitErrors(d, t)
  const save = async () => {
    setErr(undefined)
    try {
      await api.put(`/vms/${encodeURIComponent(vm.name)}/cloudinit`, toCloudInit(d))
      toast('ok', t('ci.saved'))
      setD({ ...d, password: '', keepPassword: d.keepPassword || !!d.password })
      onChanged()
    } catch (e) {
      setErr(e as ApiError)
    }
  }
  return (
    <div className="stack">
      <div className="panel">
        <div className="panel-body">
          <CloudInitForm d={d} set={setD} errors={errors} idPrefix="vci" />
        </div>
        {can('vm.config') && (
          <div className="dialog-foot">
            <span className="faint grow">{t('ci.apply_hint')}</span>
            <button className="btn primary" disabled={Object.keys(errors).length > 0} onClick={save}>{t('common.save')}</button>
          </div>
        )}
      </div>
      <ErrorAlert error={err} />
    </div>
  )
}
