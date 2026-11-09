import { cleanup, render, renderHook, screen } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import type { ReactNode } from "react"
import { afterEach, describe, expect, it } from "vitest"
import type { ModelSwitchInfo } from "@/lib/types"
import { useExportLabels } from "@/lib/use-export-labels"
import en from "@/i18n/messages/en.json"
import zhCN from "@/i18n/messages/zh-CN.json"
import zhTW from "@/i18n/messages/zh-TW.json"
import ja from "@/i18n/messages/ja.json"
import ko from "@/i18n/messages/ko.json"
import es from "@/i18n/messages/es.json"
import de from "@/i18n/messages/de.json"
import fr from "@/i18n/messages/fr.json"
import pt from "@/i18n/messages/pt.json"
import ar from "@/i18n/messages/ar.json"
import { ModelSwitchRow } from "./model-switch-row"

const record: ModelSwitchInfo = {
  id: "switch-1",
  connection_id: "connection-1",
  session_id: null,
  sequence: 1,
  config_id: "model",
  previous_model_id: null,
  previous_model_name: null,
  model_id: "provider/new",
  model_name: null,
  requested_model_id: "provider/new",
  status: "accepted",
  created_at: "2026-11-09T10:00:00.000Z",
  after_turn_id: null,
  after_turn_started_at: null,
}

const locales = [
  {
    locale: "en",
    messages: en,
    confirmed: "Model switched",
    accepted: "Model switch requested (accepted, unconfirmed)",
    unknown: "Unknown model",
  },
  {
    locale: "zh-CN",
    messages: zhCN,
    confirmed: "已切换模型",
    accepted: "模型切换请求已接受（尚未确认生效）",
    unknown: "未知模型",
  },
  {
    locale: "zh-TW",
    messages: zhTW,
    confirmed: "已切換模型",
    accepted: "模型切換請求已接受（尚未確認生效）",
    unknown: "未知模型",
  },
  {
    locale: "ja",
    messages: ja,
    confirmed: "モデルを切り替えました",
    accepted: "モデル切替リクエストを受け付けました（適用は未確認）",
    unknown: "不明なモデル",
  },
  {
    locale: "ko",
    messages: ko,
    confirmed: "모델이 전환되었습니다",
    accepted: "모델 전환 요청이 수락되었습니다(적용 미확인)",
    unknown: "알 수 없는 모델",
  },
  {
    locale: "es",
    messages: es,
    confirmed: "Modelo cambiado",
    accepted: "Solicitud de cambio de modelo aceptada (sin confirmar)",
    unknown: "Modelo desconocido",
  },
  {
    locale: "de",
    messages: de,
    confirmed: "Modell gewechselt",
    accepted: "Modellwechsel angefragt (akzeptiert, unbestätigt)",
    unknown: "Unbekanntes Modell",
  },
  {
    locale: "fr",
    messages: fr,
    confirmed: "Modèle changé",
    accepted: "Demande de changement de modèle acceptée (non confirmée)",
    unknown: "Modèle inconnu",
  },
  {
    locale: "pt",
    messages: pt,
    confirmed: "Modelo alterado",
    accepted: "Pedido de alteração de modelo aceite (não confirmado)",
    unknown: "Modelo desconhecido",
  },
  {
    locale: "ar",
    messages: ar,
    confirmed: "تم تغيير النموذج",
    accepted: "تم قبول طلب تغيير النموذج (لم يُؤكَّد التطبيق)",
    unknown: "نموذج غير معروف",
  },
] as const

afterEach(cleanup)

describe("model-switch localization", () => {
  it.each(locales)(
    "$locale: localizes accepted and confirmed rows and export labels",
    ({ locale, messages, confirmed, accepted, unknown }) => {
      const wrapper = ({ children }: { children: ReactNode }) => (
        <NextIntlClientProvider
          locale={locale}
          messages={messages}
          timeZone="UTC"
        >
          {children}
        </NextIntlClientProvider>
      )
      const { rerender } = render(<ModelSwitchRow record={record} />, {
        wrapper,
      })
      expect(screen.getByText(accepted)).toBeInTheDocument()
      expect(screen.queryByText(confirmed)).not.toBeInTheDocument()
      expect(screen.getByText(unknown)).toBeInTheDocument()
      expect(screen.getByText("provider/new")).toBeInTheDocument()
      expect(document.querySelector("time")).toHaveAttribute(
        "datetime",
        record.created_at
      )

      rerender(<ModelSwitchRow record={{ ...record, status: "confirmed" }} />)
      expect(screen.getByText(confirmed)).toBeInTheDocument()
      expect(screen.queryByText(accepted)).not.toBeInTheDocument()

      const { result } = renderHook(useExportLabels, { wrapper })
      expect(result.current).toMatchObject({
        modelSwitchConfirmed: confirmed,
        modelSwitchAccepted: accepted,
        modelSwitchUnknown: unknown,
      })
    }
  )
})
