import { Fragment, useEffect, useState, type FunctionComponent } from "react";

interface Props {
  tips: string[];
}

export const TipOfTheDay: FunctionComponent<Props> = ({
  tips
}) => {
  const [index, setIndex] = useState(Math.floor(Math.random() * tips.length))
  useEffect(() => {
    const timer = setInterval(() => {
      setIndex(Math.floor(Math.random() * tips.length))
    }, 30000)
    return () => {
      clearInterval(timer)
    }
  })
  return (
    <Fragment>
      {tips[index]}
    </Fragment>
  )
}
