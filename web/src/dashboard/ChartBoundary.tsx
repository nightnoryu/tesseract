import { Component, type ReactNode } from "react";
export default class ChartBoundary extends Component<{children: ReactNode}, {failed: boolean}> {
  state = {failed: false};
  static getDerivedStateFromError() { return {failed: true}; }
  render() {
    return this.state.failed ? <p role="alert">The chart could not be displayed. Exact values remain available below. Reload the page to retry the chart.</p> : this.props.children;
  }
}
